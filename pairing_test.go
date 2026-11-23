package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testSSHPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh pub: %v", err)
	}
	return sshPub
}

func TestParsePairingPayload(t *testing.T) {
	uri := "herdr://pair?v=1&host=100.97.151.80&port=2222&user=root&label=kr&fp=SHA256:abc123"
	got, err := ParsePairingPayload(uri)
	if err != nil {
		t.Fatalf("ParsePairingPayload(uri): %v", err)
	}
	if got.Host != "100.97.151.80" || got.Port != 2222 || got.User != "root" || got.Label != "kr" || got.Fingerprint != "SHA256:abc123" {
		t.Errorf("unexpected uri payload: %+v", got)
	}
	if got.Target() != "100.97.151.80:2222" {
		t.Errorf("Target() = %q, want 100.97.151.80:2222", got.Target())
	}

	rawJSON := `{"v":1,"host":"mac-mini.local","user":"alice","label":"mac-mini","fp":"SHA256:xyz"}`
	got2, err := ParsePairingPayload(rawJSON)
	if err != nil {
		t.Fatalf("ParsePairingPayload(json): %v", err)
	}
	if got2.Target() != "mac-mini.local:22" || got2.User != "alice" || got2.Label != "mac-mini" {
		t.Errorf("unexpected json payload: %+v", got2)
	}

	for _, bad := range []string{"", "not-a-uri", "https://example.com", "herdr://pair?v=1&user=root", `{"user":"only"}`} {
		if _, err := ParsePairingPayload(bad); err == nil {
			t.Errorf("ParsePairingPayload(%q) unexpectedly succeeded", bad)
		}
	}
}

func TestTOFUFirstConnectAndMismatchBlock(t *testing.T) {
	store := NewMemoryTOFUStore()
	k1 := testSSHPublicKey(t)
	k2 := testSSHPublicKey(t)
	fp1 := ssh.FingerprintSHA256(k1)
	fp2 := ssh.FingerprintSHA256(k2)
	addr := &net.TCPAddr{IP: net.ParseIP("100.97.151.80"), Port: 22}

	cb := NewTOFUCallback(store, "kr", "")
	// First connect trusts silently and records fingerprint.
	if err := cb("100.97.151.80:22", addr, k1); err != nil {
		t.Fatalf("first connect failed: %v", err)
	}
	if rec, ok := store.Get("kr"); !ok || rec.Fingerprint != fp1 {
		t.Fatalf("expected recorded fp %s, got %+v (ok=%v)", fp1, rec, ok)
	}
	// Repeat with same key succeeds.
	if err := cb("100.97.151.80:22", addr, k1); err != nil {
		t.Fatalf("repeat connect failed: %v", err)
	}
	// Changed key MUST hard-block with HostKeyMismatchError carrying old & new fingerprints.
	err := cb("100.97.151.80:22", addr, k2)
	var mismatch *HostKeyMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected *HostKeyMismatchError, got %v", err)
	}
	if mismatch.Expected != fp1 || mismatch.Got != fp2 {
		t.Errorf("mismatch fingerprints = (%s, %s), want (%s, %s)", mismatch.Expected, mismatch.Got, fp1, fp2)
	}
	// Revoking allows trusting the new key.
	store.Revoke("kr")
	if err := cb("100.97.151.80:22", addr, k2); err != nil {
		t.Fatalf("connect after revoke failed: %v", err)
	}

	// QR pre-pinned fingerprint rejects a mismatched key on the VERY FIRST connect.
	cbPinned := NewTOFUCallback(store, "mac-mini", fp1)
	if err := cbPinned("mac-mini:22", addr, k2); !errors.As(err, &mismatch) {
		t.Fatalf("pre-pinned mismatch should return HostKeyMismatchError, got %v", err)
	}
	if err := cbPinned("mac-mini:22", addr, k1); err != nil {
		t.Fatalf("pre-pinned matching key failed: %v", err)
	}
}

func TestGenerateED25519KeyParsesForSSH(t *testing.T) {
	privPEM, pubLine, err := GenerateED25519Key()
	if err != nil {
		t.Fatalf("GenerateED25519Key: %v", err)
	}
	if _, err := ssh.ParsePrivateKey([]byte(privPEM)); err != nil {
		t.Fatalf("ParsePrivateKey failed: %v", err)
	}
	if !strings.HasPrefix(pubLine, "ssh-ed25519 ") {
		t.Errorf("unexpected public key format: %q", pubLine)
	}
	cmd := AuthorizedKeysCommand(pubLine)
	if !strings.Contains(cmd, "~/.ssh/authorized_keys") || !strings.Contains(cmd, pubLine) {
		t.Errorf("unexpected authorized_keys command: %q", cmd)
	}
}

func TestFallbackEncryptedSecretsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.enc")
	s, err := NewFileSecretStore(path)
	if err != nil {
		t.Fatalf("NewFileSecretStore: %v", err)
	}
	if err := s.Set("inst:kr:key", "SUPER-SECRET-PEM"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := s.Get("inst:kr:key")
	if err != nil || !ok || got != "SUPER-SECRET-PEM" {
		t.Fatalf("Get = (%q, %v, %v), want SUPER-SECRET-PEM", got, ok, err)
	}
	// Reload from disk to verify persistence & encryption.
	s2, err := NewFileSecretStore(path)
	if err != nil {
		t.Fatalf("reload NewFileSecretStore: %v", err)
	}
	got2, ok2, err := s2.Get("inst:kr:key")
	if err != nil || !ok2 || got2 != "SUPER-SECRET-PEM" {
		t.Fatalf("reloaded Get = (%q, %v, %v)", got2, ok2, err)
	}
	if err := s2.Delete("inst:kr:key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok3, err := s2.Get("inst:kr:key"); err != nil || ok3 {
		t.Fatalf("expected deleted secret absent, got ok=%v err=%v", ok3, err)
	}
}
