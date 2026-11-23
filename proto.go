package main

import "encoding/json"

// Status is the program's own status record (7501): its words, untranslated.
type Status struct {
	State    string `json:"state"` // idle, working, done, blocked, error
	Kind     string `json:"kind"`  // permission, question or auth (blocked only)
	Title    string `json:"title"`
	Msg      string `json:"msg"`
	App      string `json:"app"`
	Progress *int   `json:"progress"`
}

// Terminal is one terminal as terminal.list and terminal.get return it.
type Terminal struct {
	Address    string            `json:"address"`
	Name       string            `json:"name"`
	Title      string            `json:"title"`
	State      string            `json:"state"`
	Status     *Status           `json:"status"`
	Cwd        string            `json:"cwd"`
	Parent     *string           `json:"parent"`
	ExitCode   *int              `json:"exit_code"`
	Signal     *string           `json:"signal"`
	FinishedMS *uint64           `json:"finished_ms"`
	CreatedMS  uint64            `json:"created_ms"`
	StatusMS   *uint64           `json:"status_ms"`
	Turn       uint64            `json:"turn"`
	Cols       int               `json:"cols"`
	Rows       int               `json:"rows"`
	Labels     map[string]string `json:"labels"`
	Reason     *string           `json:"reason"`
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
func (t Terminal) Finished() bool { return t.FinishedMS != nil || t.ExitCode != nil }

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

// WaitResult is terminal.wait. Reason is status, exit, output, timeout,
// not_started or message; with children: children, message.
type WaitResult struct {
	Reason   string  `json:"reason"`
	Ended    bool    `json:"ended"`
	ExitCode *int    `json:"exit_code"`
	Signal   *string `json:"signal"`
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
	Name     string `json:"name"`
	ID       string `json:"id"`
	Host     string `json:"host"`
	Target   string `json:"target"`
	Session  *string `json:"session"`
	Attempts int    `json:"attempts"`
	Reason   string `json:"reason"`
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

// Host is a saved way to reach a core.
type Host struct {
	Label    string `json:"label"`
	Target   string `json:"target"` // host:port, or "local"
	User     string `json:"user"`
	Password string `json:"password"`
	// Key is an OpenSSH private key, used before Password. Chat apps and
	// forms mangle its newlines, so a body pasted without them is rebuilt.
	Key string `json:"key"`
}
