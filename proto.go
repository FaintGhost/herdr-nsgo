package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// Status is the program's own status record (OSC 7501): its words, untranslated.
type Status struct {
	State    string `json:"state"` // idle, working, done, blocked, error
	Kind     string `json:"kind"`  // permission, question or auth (blocked only)
	Title    string `json:"title"`
	Msg      string `json:"msg"`
	App      string `json:"app"`
	Progress *int   `json:"progress"`
}

// Foreground describes the foreground process inside a terminal.
type Foreground struct {
	PID  uint32   `json:"pid"`
	Name string   `json:"name"`
	Cwd  string   `json:"cwd"`
	Argv []string `json:"argv,omitempty"`
}

// Terminal is one terminal as terminal.list, terminal.get, and terminal.run return it.
type Terminal struct {
	Address    string            `json:"address"`
	Argv       []string          `json:"argv,omitempty"`
	PID        *uint32           `json:"pid,omitempty"`
	Name       string            `json:"name"`
	Title      string            `json:"title"`
	State      string            `json:"state"`
	Status     *Status           `json:"status"`
	Foreground *Foreground       `json:"foreground"`
	Cwd        string            `json:"cwd"`
	Parent     *string           `json:"parent"`
	ExitCode   *int              `json:"exit_code"`
	Signal     json.RawMessage   `json:"signal"`
	FinishedMS *uint64           `json:"finished_ms"`
	CreatedMS  uint64            `json:"created_ms"`
	StatusMS   *uint64           `json:"status_ms"`
	Turn       uint64            `json:"turn"`
	Cols       int               `json:"cols"`
	Rows       int               `json:"rows"`
	Labels     map[string]string `json:"labels"`
	Reason     *string           `json:"reason"`
	Template   *string           `json:"template"`
}

// SplitMachineAddr splits a federated terminal address ("kr/t12") into ("kr", "t12"),
// or ("", "t9") for a local terminal on the connected core.
func SplitMachineAddr(addr string) (on, local string) {
	if prefix, sub, ok := strings.Cut(addr, "/"); ok && prefix != "" && sub != "" {
		return prefix, sub
	}
	return "", addr
}

// cleanSessionTitle strips redundant CLI prompt/brand prefixes like "π - " from terminal window titles
// so Gemini/other harnesses don't inherit a stale "π" prefix and Pi sessions pair cleanly with the colorful P icon.
func cleanSessionTitle(raw string) string {
	s := strings.TrimSpace(raw)
	for _, p := range []string{"π - ", "π – ", "π — ", "π · ", "π: ", "π "} {
		if strings.HasPrefix(s, p) {
			trimmed := strings.TrimSpace(strings.TrimPrefix(s, p))
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return s
}

// Label is what a row shows: the caller's name, else the title, else the address.
func (t Terminal) Label() string {
	if t.Name != "" {
		return cleanSessionTitle(t.Name)
	}
	if supportedMux(t.Labels[muxKindLabel]) && t.Labels[muxNameLabel] != "" {
		return cleanSessionTitle(t.Labels[muxNameLabel])
	}
	if t.Title != "" {
		return cleanSessionTitle(t.Title)
	}
	return t.Address
}

// CurrentDir returns the live foreground directory if known, else launch cwd.
func (t Terminal) CurrentDir() string {
	if t.Foreground != nil && t.Foreground.Cwd != "" {
		return t.Foreground.Cwd
	}
	return t.Cwd
}

// StateWord is the program's own status word, else what the record implies.
func (t Terminal) StateWord() string {
	if t.Status != nil && t.Status.State != "" {
		return t.Status.State
	}
	if t.State != "" {
		return t.State
	}
	if t.FinishedMS != nil {
		return "done"
	}
	return "idle"
}

// NeedsYou reports a terminal waiting on a person: blocked (permission,
// question, auth) or error. This is the inbox's whole definition.
func (t Terminal) NeedsYou() bool {
	switch t.StateWord() {
	case "blocked", "error":
		return true
	}
	return false
}

// ActiveMS returns the most recent known activity timestamp (ms) reported by herdr-core for this terminal.
func (t Terminal) ActiveMS() uint64 {
	best := t.CreatedMS
	if t.StatusMS != nil && *t.StatusMS > best {
		best = *t.StatusMS
	}
	if t.FinishedMS != nil && *t.FinishedMS > best {
		best = *t.FinishedMS
	}
	return best
}

// Finished reports a terminal whose program is gone.
func (t Terminal) Finished() bool {
	return t.FinishedMS != nil || t.ExitCode != nil || t.State == "finished"
}

// ReadResult is terminal.read: the screen plus history, as text.
type ReadResult struct {
	Text     string  `json:"text"`
	Complete bool    `json:"complete"`
	How      string  `json:"how"`
	Why      *string `json:"why"`
}

// SendResult is terminal.send.
type SendResult struct {
	Turn    uint64      `json:"turn"`
	Started *bool       `json:"started"`
	Status  *string     `json:"status"`
	Wait    *WaitResult `json:"wait"`
}

// WaitResult is terminal.wait.
type WaitResult struct {
	Reason   string          `json:"reason"`
	Ended    bool            `json:"ended"`
	ExitCode *int            `json:"exit_code"`
	Signal   json.RawMessage `json:"signal"`
	Status   *Status         `json:"status"`
	From     *string         `json:"from"`
}

// KeysResult is terminal.keys.
type KeysResult struct {
	Turn   uint64  `json:"turn"`
	Status *string `json:"status"`
}

// MachineView is a machine as machine.list shows it.
type MachineView struct {
	Name     string  `json:"name"`
	ID       string  `json:"id"`
	Host     string  `json:"host"`
	Target   string  `json:"target"`
	Session  *string `json:"session"`
	Attempts int     `json:"attempts"`
	Reason   string  `json:"reason"`
}

// ListResult is terminal.list.
type ListResult struct {
	Terminals      []Terminal    `json:"terminals"`
	HiddenFinished int           `json:"hidden_finished"`
	Machines       []MachineView `json:"machines"`
}

// MachineListResult is machine.list.
type MachineListResult struct {
	Machines []MachineView `json:"machines"`
}

// CoreInfo names the core on the other end.
type CoreInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// HelloResult is the handshake's answer.
type HelloResult struct {
	Generation uint32   `json:"generation"`
	Framed     bool     `json:"framed"`
	Version    string   `json:"version"`
	Features   []string `json:"features"`
	Methods    []string `json:"methods"`
	Core       CoreInfo `json:"core"`
}

// SubscribeResult is events.subscribe: a snapshot, then events.
type SubscribeResult struct {
	Channel   *uint32    `json:"channel"`
	Seq       uint64     `json:"seq"`
	Terminals []Terminal `json:"terminals"`
}

// Event is one notification from events.subscribe.
type Event struct {
	Type     string          `json:"type"`
	Terminal string          `json:"terminal"`
	Seq      uint64          `json:"seq"`
	Data     json.RawMessage `json:"data"`
}

// Display reports the viewer's terminal colors and cell geometry to herdr-core.
type Display struct {
	FG     string     `json:"fg,omitempty"`
	BG     string     `json:"bg,omitempty"`
	Cursor string     `json:"cursor,omitempty"`
	Cell   *[2]uint32 `json:"cell,omitempty"`
}

// InitialSize configures cols and rows for terminal.run.
type InitialSize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// AttachParams configures terminal.attach on a framed connection.
type AttachParams struct {
	Terminal            string   `json:"terminal"`
	Cols                int      `json:"cols"`
	Rows                int      `json:"rows"`
	History             int      `json:"history,omitempty"`
	Observe             bool     `json:"observe,omitempty"`
	ResizeNotifications bool     `json:"resize_notifications,omitempty"`
	Resumed             bool     `json:"resumed,omitempty"`
	Display             *Display `json:"display,omitempty"`
}

// AttachResult is terminal.attach's reply.
type AttachResult struct {
	Channel             uint32   `json:"channel"`
	ResizeNotifications bool     `json:"resize_notifications"`
	AutomaticInput      bool     `json:"automatic_input"`
	Terminal            Terminal `json:"terminal"`
}

// RunParams configures terminal.run.
type RunParams struct {
	Argv     []string          `json:"argv,omitempty"`
	Line     string            `json:"line,omitempty"`
	Cwd      string            `json:"cwd,omitempty"`
	Name     string            `json:"name,omitempty"`
	On       string            `json:"on,omitempty"`
	Keep     bool              `json:"keep,omitempty"`
	NoNotify bool              `json:"no_notify,omitempty"`
	Size     *InitialSize      `json:"size,omitempty"`
	Display  *Display          `json:"display,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

// FirstMessage reports whether a template launch's initial task was delivered.
type FirstMessage struct {
	Sent    bool    `json:"sent"`
	Started *bool   `json:"started"`
	Reason  *string `json:"reason"`
}

// RunResult is terminal.run's reply.
type RunResult struct {
	Terminal
	FirstMessage *FirstMessage `json:"first_message"`
}

// Template is one entry from templates.list.
type Template struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// TemplatesResult is templates.list's reply.
type TemplatesResult struct {
	Templates []Template `json:"templates"`
}

// PutResult is terminal.put / terminal.put_finish's reply.
type PutResult struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
}

// FileReadParams configures file.read (read-only line window on core or federated machine).
type FileReadParams struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"` // 1-based first line
	Limit  int    `json:"limit,omitempty"`
	On     string `json:"on,omitempty"`
	Cwd    string `json:"cwd,omitempty"`
}

// FileReadResult is file.read's reply.
type FileReadResult struct {
	Text  string `json:"text"`
	First int    `json:"first"`
	Last  int    `json:"last"`
	Lines int    `json:"lines"`
}

// ExecParams configures machine.exec (short read-only discovery commands; Timeout is in seconds).
type ExecParams struct {
	Argv    []string          `json:"argv,omitempty"`
	Line    string            `json:"line,omitempty"`
	On      string            `json:"on,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Input   string            `json:"input,omitempty"`
	Timeout float64           `json:"timeout,omitempty"`
}

// ExecResult is machine.exec's reply (Stdout and Stderr are Base64-encoded).
type ExecResult struct {
	Code      *int32 `json:"code"`
	Signal    *int32 `json:"signal"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	MS        uint64 `json:"ms"`
	TimedOut  bool   `json:"timed_out"`
	Truncated bool   `json:"truncated"`
}

// StdoutText decodes the Base64 stdout payload from machine.exec.
func (r ExecResult) StdoutText() string {
	if r.Stdout == "" {
		return ""
	}
	b, err := base64.StdEncoding.DecodeString(r.Stdout)
	if err != nil {
		return ""
	}
	return string(b)
}

// StderrText decodes the Base64 stderr payload from machine.exec.
func (r ExecResult) StderrText() string {
	if r.Stderr == "" {
		return ""
	}
	b, err := base64.StdEncoding.DecodeString(r.Stderr)
	if err != nil {
		return ""
	}
	return string(b)
}

// Host is a saved way to reach a core (kept compatible with v0.1.0 fields).
type Host struct {
	ID       string `json:"id,omitempty"`
	Label    string `json:"label"`
	Target   string `json:"target"` // host:port, or "local"
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	// Key is an OpenSSH private key, used before Password.
	Key string `json:"key,omitempty"`
	// PinnedFP optionally pins the host SSH key SHA256 fingerprint from QR pairing.
	PinnedFP string `json:"pinned_fp,omitempty"`
	// UseLocalKey tells transport to authenticate using the app-managed ed25519 key.
	UseLocalKey bool `json:"use_local_key,omitempty"`
	// Enabled controls whether CorePool keeps this instance connected.
	Enabled *bool `json:"enabled,omitempty"`
	// CredRev is bumped on credential edits so CorePool restarts the worker.
	CredRev int64 `json:"cred_rev,omitempty"`
}

// IsEnabled reports whether this host should participate in CorePool.
func (h Host) IsEnabled() bool {
	if h.Enabled == nil {
		return true
	}
	return *h.Enabled
}

// EffectiveID returns a stable identifier for this host configuration.
func (h Host) EffectiveID() string {
	if h.ID != "" {
		return h.ID
	}
	if h.Label != "" {
		return h.Label
	}
	return h.Target
}

// InstanceConfig is an alias emphasizing multi-instance pool usage.
type InstanceConfig = Host
