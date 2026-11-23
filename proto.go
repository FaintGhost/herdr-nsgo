package main

import "encoding/json"

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
	PID  uint32 `json:"pid"`
	Name string `json:"name"`
	Cwd  string `json:"cwd"`
}

// Terminal is one terminal as terminal.list, terminal.get, and terminal.run return it.
type Terminal struct {
	Address    string            `json:"address"`
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

// Label is what a row shows: the caller's name, else the title, else the address.
func (t Terminal) Label() string {
	if t.Name != "" {
		return t.Name
	}
	if t.Title != "" {
		return t.Title
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

// Finished reports a terminal whose program is gone.
func (t Terminal) Finished() bool { return t.FinishedMS != nil || t.ExitCode != nil || t.State == "finished" }

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
	Reason   string  `json:"reason"`
	Ended    bool    `json:"ended"`
	ExitCode *int            `json:"exit_code"`
	Signal   json.RawMessage `json:"signal"`
	Status   *Status `json:"status"`
	From     *string `json:"from"`
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
	FG     string    `json:"fg,omitempty"`
	BG     string    `json:"bg,omitempty"`
	Cursor string    `json:"cursor,omitempty"`
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
