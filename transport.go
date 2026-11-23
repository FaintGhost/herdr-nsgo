package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// conn is a transcript with a core: bytes out are requests, bytes in are
// answers and notifications.
type conn struct {
	io.ReadWriteCloser
	closers []func() error
}

func (c *conn) Close() error {
	err := c.ReadWriteCloser.Close()
	for _, f := range c.closers {
		if e := f(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

// dialSSH runs `hc connect` on the far side using TOFU host key pinning.
func dialSSH(h Host, tofu *TOFUStore) (io.ReadWriteCloser, error) {
	auth, err := authFor(h)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            h.User,
		Auth:            auth,
		HostKeyCallback: NewTOFUCallback(tofu, h.EffectiveID(), h.PinnedFP),
		Timeout:         15 * time.Second,
	}
	addr := withPort(h.Target)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh %s: %w", addr, err)
	}
	sess, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("ssh session: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		client.Close()
		return nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		client.Close()
		return nil, err
	}
	if err := sess.Start("hc connect"); err != nil {
		sess.Close()
		client.Close()
		return nil, fmt.Errorf("running `hc connect` on the host: %w", err)
	}
	return &conn{
		ReadWriteCloser: readWrite{stdout, stdin},
		closers:         []func() error{sess.Close, client.Close},
	}, nil
}

// withPort adds SSH's port when a host names none, so "box" and
// "box.example.com" both work and "box:2222" keeps its port.
func withPort(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return target
	}
	if _, _, err := net.SplitHostPort(target); err == nil {
		return target
	}
	return net.JoinHostPort(strings.Trim(target, "[]"), "22")
}

// authFor prefers the saved key, then the password.
func authFor(h Host) ([]ssh.AuthMethod, error) {
	if key := strings.TrimSpace(h.Key); key != "" {
		signer, err := ssh.ParsePrivateKey([]byte(normalizeKey(key)))
		if err != nil {
			return nil, fmt.Errorf("私钥读不了（%v）。重新粘贴完整密钥，或改用密码。", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	}
	if h.Password != "" {
		return []ssh.AuthMethod{ssh.Password(h.Password)}, nil
	}
	return nil, errors.New("没有凭据：填密码，或使用本机 SSH 密钥 / 粘贴 OpenSSH 私钥")
}

// normalizeKey rebuilds a key whose newlines a paste or a form dropped:
// a bare base64 body gets its header, footer and 70-column lines back.
func normalizeKey(key string) string {
	if strings.Contains(key, "-----BEGIN") {
		lines := strings.Split(key, "\n")
		for i, l := range lines {
			if len(l) > 70 && !strings.Contains(l, "-----") {
				lines[i] = wrap70(l)
			}
		}
		return strings.Join(lines, "\n") + "\n"
	}
	body := strings.Join(strings.Fields(key), "")
	if body == "" {
		return key
	}
	const head = "-----BEGIN OPENSSH PRIVATE KEY-----"
	const foot = "-----END OPENSSH PRIVATE KEY-----"
	return head + "\n" + wrap70(body) + "\n" + foot + "\n"
}

func wrap70(s string) string {
	var b strings.Builder
	for len(s) > 70 {
		b.WriteString(s[:70])
		b.WriteString("\n")
		s = s[70:]
	}
	b.WriteString(s)
	return b.String()
}

// dialLocal runs `hc connect` here, for a desktop build or a dev loop.
func dialLocal() (io.ReadWriteCloser, error) {
	path, err := exec.LookPath("hc")
	if err != nil {
		return nil, errors.New("hc is not on this machine's PATH")
	}
	cmd := exec.Command(path, "connect")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &conn{
		ReadWriteCloser: readWrite{stdout, stdin},
		closers:         []func() error{cmd.Process.Kill, func() error { return cmd.Wait() }},
	}, nil
}

// DialWithTOFU opens the transport a host names using tofu host-key verification.
func DialWithTOFU(h Host, tofu *TOFUStore) (io.ReadWriteCloser, error) {
	if h.Target == "" || h.Target == "local" {
		return dialLocal()
	}
	return dialSSH(h, tofu)
}

// Dial opens the transport a host names using an ephemeral TOFU store.
func Dial(h Host) (io.ReadWriteCloser, error) {
	return DialWithTOFU(h, nil)
}

// readWrite pairs a reader and a writer into one stream.
type readWrite struct {
	io.Reader
	io.Writer
}

// Close closes the writer, which is the half that carries the request.
func (r readWrite) Close() error {
	if c, ok := r.Writer.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
