package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/nsgo-dev/nsgo/core"
)

// SecretBackend abstracts OS-protected secret storage (Android Keystore / iOS Keychain)
// and an encrypted local-file fallback when platform secrets are unavailable (e.g. headless tests).
type SecretBackend interface {
	Get(account string) (string, bool, error)
	Set(account, value string) error
	Delete(account string) error
}

// nsgoSecretStore wraps NSGo's native core.Secrets (Android Keystore / iOS Keychain / macOS Keychain)
// and falls back transparently to an encrypted local store only when core.Secrets returns an error.
type nsgoSecretStore struct {
	native   core.Secrets
	fallback SecretBackend
}

// NewSecretStore creates the app's SecretBackend backed by NSGo's OS Keystore/Keychain,
// with a pure-Go AES-256-GCM fallback in the user cache/config dir if OS secrets are unavailable.
func NewSecretStore(cx *core.Scope) SecretBackend {
	var fb SecretBackend
	if dir, err := os.UserConfigDir(); err == nil {
		if f, err := NewFileSecretStore(filepath.Join(dir, "herdr-nsgo", "secrets.enc")); err == nil {
			fb = f
		}
	}
	if fb == nil {
		fb = newMemSecretStore()
	}
	if cx == nil {
		return fb
	}
	return &nsgoSecretStore{
		native:   cx.Secrets(),
		fallback: fb,
	}
}

func (s *nsgoSecretStore) Get(account string) (string, bool, error) {
	v, ok, err := s.native.Get(account)
	if err == nil {
		if ok {
			return v, true, nil
		}
		return s.fallback.Get(account)
	}
	return s.fallback.Get(account)
}

func (s *nsgoSecretStore) Set(account, value string) error {
	if err := s.native.Set(account, value); err == nil {
		return nil
	}
	return s.fallback.Set(account, value)
}

func (s *nsgoSecretStore) Delete(account string) error {
	err1 := s.native.Delete(account)
	err2 := s.fallback.Delete(account)
	if err1 != nil && err2 != nil {
		return err1
	}
	return nil
}

// FileSecretStore is a pure-Go AES-256-GCM encrypted key-value store on disk.
type FileSecretStore struct {
	mu      sync.Mutex
	path    string
	keyPath string
	key     []byte
	items   map[string]string
}

// NewFileSecretStore opens or creates an AES-256-GCM encrypted secrets file at path.
func NewFileSecretStore(path string) (*FileSecretStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	keyPath := path + ".key"
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, err
	}
	s := &FileSecretStore{
		path:    path,
		keyPath: keyPath,
		key:     key,
		items:   make(map[string]string),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func loadOrCreateKey(path string) ([]byte, error) {
	if raw, err := os.ReadFile(path); err == nil && len(raw) == 32 {
		return raw, nil
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func (s *FileSecretStore) load() error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) || len(raw) == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	if len(raw) < gcm.NonceSize() {
		return errors.New("corrupt encrypted secrets file")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, &s.items)
}

func (s *FileSecretStore) saveLocked() error {
	plain, err := json.Marshal(s.items)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	sealed := gcm.Seal(nonce, nonce, plain, nil)
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, sealed, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *FileSecretStore) Get(account string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.items[account]
	return v, ok, nil
}

func (s *FileSecretStore) Set(account, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[account] = value
	return s.saveLocked()
}

func (s *FileSecretStore) Delete(account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, account)
	return s.saveLocked()
}

type memSecretStore struct {
	mu    sync.Mutex
	items map[string]string
}

func newMemSecretStore() *memSecretStore {
	return &memSecretStore{items: make(map[string]string)}
}

func (m *memSecretStore) Get(account string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.items[account]
	return v, ok, nil
}

func (m *memSecretStore) Set(account, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[account] = value
	return nil
}

func (m *memSecretStore) Delete(account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, account)
	return nil
}
