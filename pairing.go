package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"golang.org/x/crypto/ssh"
)

// PairingPayload represents a decoded QR code pairing payload (URI or JSON).
type PairingPayload struct {
	Version     int    `json:"v"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	Label       string `json:"label"`
	Fingerprint string `json:"fp"`
	Key         string `json:"key,omitempty"`
	Password    string `json:"password,omitempty"`
}

// Target returns host:port with default port 22.
func (p PairingPayload) Target() string {
	port := p.Port
	if port <= 0 {
		port = 22
	}
	return net.JoinHostPort(strings.Trim(strings.TrimSpace(p.Host), "[]"), strconv.Itoa(port))
}

// ParsePairingPayload parses either a herdr://pair?v=1&... URI or a compact JSON object.
func ParsePairingPayload(raw string) (PairingPayload, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return PairingPayload{}, errors.New("配对内容为空")
	}

	var p PairingPayload
	if strings.HasPrefix(raw, "{") {
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return PairingPayload{}, fmt.Errorf("配对 JSON 格式错误：%w", err)
		}
	} else {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "herdr" || u.Host != "pair" {
			return PairingPayload{}, errors.New("不支持的配对二维码格式（应为 herdr://pair?... 或 JSON）")
		}
		q := u.Query()
		p.Host = strings.TrimSpace(q.Get("host"))
		p.User = strings.TrimSpace(q.Get("user"))
		p.Label = strings.TrimSpace(q.Get("label"))
		p.Fingerprint = strings.TrimSpace(q.Get("fp"))
		p.Key = strings.TrimSpace(q.Get("key"))
		p.Password = q.Get("password")
		if v := q.Get("v"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				p.Version = n
			}
		}
		if portStr := q.Get("port"); portStr != "" {
			port, err := strconv.Atoi(portStr)
			if err != nil || port <= 0 || port > 65535 {
				return PairingPayload{}, errors.New("配对端口无效")
			}
			p.Port = port
		}
	}

	if p.Version == 0 {
		p.Version = 1
	}
	if p.Host == "" {
		return PairingPayload{}, errors.New("配对内容缺少主机地址 host")
	}
	if p.Port == 0 {
		p.Port = 22
	}
	if p.Label == "" {
		p.Label = p.Host
	}
	return p, nil
}

// GenerateED25519Key creates a fresh OpenSSH ed25519 keypair for passwordless auth.
func GenerateED25519Key() (privPEM, pubLine string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, "herdr-nsgo")
	if err != nil {
		return "", "", err
	}
	privPEM = string(pem.EncodeToMemory(block))
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	pubLine = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " herdr-nsgo"
	return privPEM, pubLine, nil
}

// AuthorizedKeysCommand formats a single-line shell command to authorize pubLine on a remote host.
func AuthorizedKeysCommand(pubLine string) string {
	clean := strings.TrimSpace(pubLine)
	return fmt.Sprintf(`mkdir -p ~/.ssh && chmod 700 ~/.ssh && echo %q >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys`, clean)
}

// TOFURecord stores one pinned SSH host key fingerprint.
type TOFURecord struct {
	HostID      string `json:"host_id"`
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
	FirstSeenMS int64  `json:"first_seen_ms"`
}

// HostKeyMismatchError is returned when a host's SSH public key differs from its pinned fingerprint.
type HostKeyMismatchError struct {
	HostID   string
	Address  string
	Expected string
	Got      string
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("SSH 主机指纹变更（%s）：原 %s，现 %s。为防中间人攻击已阻断连接，请在设置中核实并撤销旧指纹后重连。", e.HostID, e.Expected, e.Got)
}

const tofuStorageKey = "herdr-nsgo.tofu.v1"

// TOFUStore persists trusted SSH host SHA256 fingerprints.
type TOFUStore struct {
	mu      sync.Mutex
	records map[string]TOFURecord
	save    func(map[string]TOFURecord)
	onEdit  func()
}

// NewMemoryTOFUStore returns an in-memory TOFUStore (useful for tests).
func NewMemoryTOFUStore() *TOFUStore {
	return &TOFUStore{records: make(map[string]TOFURecord)}
}

// NewStorageTOFUStore loads and persists TOFU fingerprints in NSGo's core.Storage.
func NewStorageTOFUStore(cx *core.Scope) *TOFUStore {
	s := NewMemoryTOFUStore()
	if cx == nil {
		return s
	}
	if raw, ok := cx.Storage().GetItem(tofuStorageKey); ok && raw != "" {
		var list []TOFURecord
		if json.Unmarshal([]byte(raw), &list) == nil {
			for _, rec := range list {
				s.records[rec.HostID] = rec
			}
		}
	}
	s.save = func(m map[string]TOFURecord) {
		list := make([]TOFURecord, 0, len(m))
		for _, rec := range m {
			list = append(list, rec)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].HostID < list[j].HostID })
		if raw, err := json.Marshal(list); err == nil {
			cx.Storage().SetItem(tofuStorageKey, string(raw))
		}
	}
	return s
}

// OnChange sets a listener invoked after any record is pinned or revoked.
func (s *TOFUStore) OnChange(fn func()) {
	s.mu.Lock()
	s.onEdit = fn
	s.mu.Unlock()
}

// Get returns the pinned record for hostID, if present.
func (s *TOFUStore) Get(hostID string) (TOFURecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[hostID]
	return r, ok
}

// List returns all trusted host fingerprint records sorted by HostID.
func (s *TOFUStore) List() []TOFURecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TOFURecord, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].HostID < out[j].HostID })
	return out
}

// Pin saves a host fingerprint.
func (s *TOFUStore) Pin(rec TOFURecord) {
	s.mu.Lock()
	s.records[rec.HostID] = rec
	save := s.save
	onEdit := s.onEdit
	snapshot := make(map[string]TOFURecord, len(s.records))
	for k, v := range s.records {
		snapshot[k] = v
	}
	s.mu.Unlock()
	if save != nil {
		save(snapshot)
	}
	if onEdit != nil {
		onEdit()
	}
}

// Revoke deletes the trusted fingerprint for hostID so the next connect can re-pin.
func (s *TOFUStore) Revoke(hostID string) {
	s.mu.Lock()
	delete(s.records, hostID)
	save := s.save
	onEdit := s.onEdit
	snapshot := make(map[string]TOFURecord, len(s.records))
	for k, v := range s.records {
		snapshot[k] = v
	}
	s.mu.Unlock()
	if save != nil {
		save(snapshot)
	}
	if onEdit != nil {
		onEdit()
	}
}

// NewTOFUCallback returns an ssh.HostKeyCallback enforcing silent first-connect TOFU,
// strict pre-pin verification when pinnedFP is provided (e.g. via QR), and hard-block
// with *HostKeyMismatchError whenever a host's fingerprint changes.
func NewTOFUCallback(store *TOFUStore, hostID, pinnedFP string) ssh.HostKeyCallback {
	if store == nil {
		store = NewMemoryTOFUStore()
	}
	pinnedFP = strings.TrimSpace(pinnedFP)
	return func(hostname string, _ net.Addr, key ssh.PublicKey) error {
		gotFP := ssh.FingerprintSHA256(key)
		keyID := hostID
		if keyID == "" {
			keyID = hostname
		}
		if rec, ok := store.Get(keyID); ok {
			if rec.Fingerprint != gotFP {
				return &HostKeyMismatchError{
					HostID:   keyID,
					Address:  hostname,
					Expected: rec.Fingerprint,
					Got:      gotFP,
				}
			}
			return nil
		}
		if pinnedFP != "" && pinnedFP != gotFP {
			return &HostKeyMismatchError{
				HostID:   keyID,
				Address:  hostname,
				Expected: pinnedFP,
				Got:      gotFP,
			}
		}
		store.Pin(TOFURecord{
			HostID:      keyID,
			Address:     hostname,
			Fingerprint: gotFP,
			FirstSeenMS: time.Now().UnixMilli(),
		})
		return nil
	}
}
