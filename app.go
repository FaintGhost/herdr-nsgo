package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nsgo-dev/nsgo/core"
)

// Connection states, as the home header shows them.
const (
	stateOff      = "disconnected"
	stateDialing  = "connecting"
	stateOnline   = "connected"
	stateFailed   = "error"
	stateReplaced = "connected"
)

// App holds what every screen reads: the connection, the terminals, and the
// text of the session being watched.
type App struct {
	cx *core.Scope

	conn     *core.Signal[string]
	problem  *core.Signal[string]
	coreName *core.Signal[string]
	terms    *core.Signal[[]Terminal]
	machines *core.Signal[[]MachineView]
	texts    *core.Signal[map[string]string]
	composes *core.Signal[map[string]string]
	filter   *core.Signal[string]

	mu       sync.Mutex
	client   *Client
	host     Host
	active   string
	cancel   context.CancelFunc
	drafting map[string]string
}

// NewApp wires the state and starts the first connection if a host is saved.
func NewApp(cx *core.Scope) *App {
	a := &App{
		cx:       cx,
		conn:     core.NewSignal(cx, stateOff),
		problem:  core.NewSignal(cx, ""),
		coreName: core.NewSignal(cx, ""),
		terms:    core.NewSignalFunc(cx, []Terminal(nil), nil),
		machines: core.NewSignalFunc(cx, []MachineView(nil), nil),
		texts:    core.NewSignalFunc(cx, map[string]string{}, nil),
		composes: core.NewSignalFunc(cx, map[string]string{}, nil),
		filter:   core.NewSignal(cx, ""),
	}
	a.cx.Cleanup(a.disconnect)
	if hosts := a.loadHosts(); len(hosts) > 0 {
		a.host = hosts[0]
		a.connect(hosts[0])
	}
	return a
}

// ---------------------------------------------------------------------------
// hosts

const hostsKey = "herdr-nsgo.hosts"

func (a *App) loadHosts() []Host {
	raw, ok := a.cx.Storage().GetItem(hostsKey)
	if !ok || raw == "" {
		return nil
	}
	var out []Host
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil
	}
	return out
}

// SaveHost stores h, replacing one with the same label.
func (a *App) SaveHost(h Host) {
	hosts := a.loadHosts()
	kept := hosts[:0]
	for _, old := range hosts {
		if old.Label != h.Label {
			kept = append(kept, old)
		}
	}
	kept = append(kept, h)
	if raw, err := json.Marshal(kept); err == nil {
		a.cx.Storage().SetItem(hostsKey, string(raw))
	}
	a.host = h
	a.connect(h)
}

// ForgetHost drops the saved host and disconnects.
func (a *App) ForgetHost() {
	a.cx.Storage().RemoveItem(hostsKey)
	a.disconnect()
	a.conn.Set(stateOff)
}

// Host is the host in use.
func (a *App) Host() Host {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.host
}

// ---------------------------------------------------------------------------
// connection

// connect replaces any connection with one to h.
func (a *App) connect(h Host) {
	a.disconnect()
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.cancel = cancel
	a.mu.Unlock()
	a.conn.Set(stateDialing)
	a.problem.Set("")

	go func() {
		rw, err := Dial(h)
		if err != nil {
			a.fail(err)
			return
		}
		c := NewClient(rw)
		c.OnEvent(func(ev Event) { a.onEvent(ev) })

		hctx, hcancel := context.WithTimeout(ctx, 20*time.Second)
		defer hcancel()
		hello, err := c.Hello(hctx)
		if err != nil {
			c.Close()
			a.fail(err)
			return
		}

		a.mu.Lock()
		a.client = c
		a.mu.Unlock()

		a.cx.Post(func() {
			a.coreName.Set(hello.Core.Name)
			a.conn.Set(stateOnline)
		})
		a.pump(ctx, c)
	}()
}

// disconnect ends the connection without touching the saved host.
func (a *App) disconnect() {
	a.mu.Lock()
	c, cancel := a.client, a.cancel
	a.client, a.cancel = nil, nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if c != nil {
		c.Close()
	}
}

// Reconnect dials the saved host again.
func (a *App) Reconnect() {
	h := a.Host()
	if h.Target == "" {
		a.conn.Set(stateOff)
		return
	}
	a.connect(h)
}

// pump polls the watched session and, less often, the whole list.
func (a *App) pump(ctx context.Context, c *Client) {
	a.refresh(c)
	tick := time.NewTicker(1500 * time.Millisecond)
	defer tick.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.Closed():
			a.cx.Post(func() { a.conn.Set(stateFailed) })
			a.setProblemMsg("与核心的连接断了。点这里重连。")
			return
		case <-tick.C:
			n++
			a.mu.Lock()
			active := a.active
			a.mu.Unlock()
			if active != "" {
				a.readInto(ctx, c, active)
			}
			if n%4 == 0 {
				a.refresh(c)
			}
		}
	}
}

func (a *App) refresh(c *Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	list, err := c.List(ctx, false)
	if err != nil {
		a.fail(err)
		return
	}
	machines, _ := c.Machines(ctx)
	a.cx.Post(func() {
		a.terms.Set(list.Terminals)
		if machines != nil {
			a.machines.Set(machines)
		}
	})
}

func (a *App) readInto(ctx context.Context, c *Client, addr string) {
	rctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	res, err := c.Read(rctx, addr, 300)
	if err != nil {
		a.fail(err)
		return
	}
	a.cx.Post(func() {
		next := make(map[string]string, len(a.texts.Get())+1)
		for k, v := range a.texts.Get() {
			next[k] = v
		}
		next[addr] = res.Text
		a.texts.Set(next)
	})
}

// onEvent marks the list stale so the next refresh picks the change up, and
// keeps the watched screen current without waiting for a tick.
func (a *App) onEvent(ev Event) {
	if ev.Type == "missed" || ev.Type == "created" || ev.Type == "removed" || ev.Type == "finished" {
		a.mu.Lock()
		c := a.client
		a.mu.Unlock()
		if c != nil {
			a.refresh(c)
		}
	}
}

// ---------------------------------------------------------------------------
// actions

// Watch names the session whose screen the poll loop keeps fresh.
func (a *App) Watch(addr string) {
	a.mu.Lock()
	a.active = addr
	a.mu.Unlock()
	if c := a.current(); c != nil {
		a.readInto(context.Background(), c, addr)
	}
}

// Unwatch stops refreshing a session.
func (a *App) Unwatch(addr string) {
	a.mu.Lock()
	if a.active == addr {
		a.active = ""
	}
	a.mu.Unlock()
}

func (a *App) current() *Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.client
}

// Send types text into a terminal and presses Enter.
func (a *App) Send(addr, text string) {
	c := a.current()
	if c == nil {
		a.setProblemMsg("还没连上核心。先在首页连上。")
		return
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := c.Send(ctx, addr, text); err != nil {
			a.fail(err)
			return
		}
		a.cx.Post(func() { a.setCompose(addr, "") })
		a.readInto(context.Background(), c, addr)
	}()
}

// Press sends keys by name: enter, esc, up, ctrl+c.
func (a *App) Press(addr string, keys ...string) {
	c := a.current()
	if c == nil {
		a.setProblemMsg("还没连上核心。先在首页连上。")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := c.Keys(ctx, addr, keys...); err != nil {
			a.fail(err)
			return
		}
		a.readInto(context.Background(), c, addr)
	}()
}

// Interrupt presses the stop key.
func (a *App) Interrupt(addr string) {
	c := a.current()
	if c == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := c.Interrupt(ctx, addr); err != nil {
			a.fail(err)
		}
	}()
}

// Text is the screen last read for addr.
func (a *App) Text(addr string) string { return a.texts.Get()[addr] }

// Compose is the draft typed into addr's composer.
func (a *App) Compose(addr string) string { return a.composes.Get()[addr] }

// SetCompose keeps a draft per session, so switching away loses nothing.
func (a *App) SetCompose(addr, text string) { a.setCompose(addr, text) }

func (a *App) setCompose(addr, text string) {
	next := make(map[string]string, len(a.composes.Get())+1)
	for k, v := range a.composes.Get() {
		next[k] = v
	}
	next[addr] = text
	a.composes.Set(next)
}

// Terminal returns the record for addr, if the list has it.
func (a *App) Terminal(addr string) (Terminal, bool) {
	for _, t := range a.terms.Get() {
		if t.Address == addr {
			return t, true
		}
	}
	return Terminal{Address: addr}, false
}

// Attention is the inbox: terminals waiting on a person.
func (a *App) Attention() []Terminal {
	q := strings.ToLower(strings.TrimSpace(a.filter.Get()))
	var out []Terminal
	for _, t := range a.terms.Get() {
		if !t.NeedsYou() {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(t.Label()+" "+t.Cwd+" "+t.Address), q) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// ---------------------------------------------------------------------------
// problems

func (a *App) fail(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	a.setProblemMsg(explain(err))
}

func (a *App) setProblemMsg(msg string) {
	a.cx.Post(func() {
		a.problem.Set(msg)
		// A failed dial or handshake leaves nothing connected; the banner
		// must not keep saying "connecting".
		if a.conn.Get() != stateOnline {
			a.conn.Set(stateFailed)
		}
	})
}

// explain turns a failure into the line the UI shows: what happened, then
// what to do about it. Never just "error".
func explain(err error) string {
	var re *RPCError
	if errors.As(err, &re) {
		return re.Remediation()
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no such host"):
		return "域名解析失败。检查机器地址和网络后重连。"
	case strings.Contains(msg, "connection refused"):
		return "对方拒绝了连接。确认 SSH 在跑、端口对（默认 22）后重连。"
	case strings.Contains(msg, "unable to authenticate"):
		return "SSH 认证失败。检查用户名和密码（或密钥），然后重新添加机器。"
	case strings.Contains(msg, "missing port"), strings.Contains(msg, "no such host") &&
		strings.Contains(msg, "dial tcp"):
		return "地址要写「主机」或「主机:端口」。"
	case strings.Contains(msg, "hc is not on"):
		return "本机没有 hc。桌面端要先装 herdr-core；手机端请用 SSH 连一台装了它的机器。"
	case strings.Contains(msg, "hc connect"):
		return "SSH 通了，但远端跑不起 `hc connect`：确认远端装了 herdr-core，且在 PATH 里。"
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return "连接超时。检查网络或远端负载后重连。"
	}
	return fmt.Sprintf("%s — 在首页点重连；仍失败就重启远端的 hc server。", msg)
}
