package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// RPCError is one hc failure. data.kind names it stably across versions, so
// the UI can say what to do next instead of echoing the message.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Kind string `json:"kind"`
	} `json:"data"`
	Method string `json:"-"`
}

func (e *RPCError) Error() string {
	if e.Data.Kind != "" {
		return fmt.Sprintf("%s: %s", e.Data.Kind, e.Message)
	}
	return e.Message
}

// Kind is the stable failure name, or "" when the core sent none.
func (e *RPCError) Kind() string { return e.Data.Kind }

// Remediation is the "how to fix it" line a person reads under the failure.
// Every kind names the repair, never the symptom alone.
func (e *RPCError) Remediation() string {
	switch e.Data.Kind {
	case "not_found":
		return "这个终端已经不在了。回首页刷新列表，它可能已被删除或换了地址。"
	case "machine_unknown":
		return "那台机器没有连上。检查机器列表，必要时重新添加或重连后再试。"
	case "finished":
		return "程序已经退出，不能再发送。要接着用就重新开一个终端。"
	case "blocked":
		return "程序正在等一个人操作。先在上面那一屏处理它，或按 Ctrl+C 打断后再发。"
	case "status_mismatch":
		return "程序状态和预期不一致，发送被拒绝。刷新这一屏再决定。"
	case "unavailable":
		return "核心暂时不可用（多半在重启）。等几秒重连即可。"
	case "config":
		return "核心的配置拒绝了这次请求。检查远端 ~/.config/herdr-core/config.toml。"
	case "needs_target":
		return "这条命令需要指定机器：用「机器/终端」的形式，例如 vps/t3。"
	case "invalid_params":
		return "参数不对，这是客户端的问题；升级 herdr-nsgo 或看日志。"
	case "cancelled":
		return "操作被取消（可能有别人同时在操作这个终端）。重试一次。"
	}
	if e.Message != "" {
		return "检查连接后在首页重试；仍失败就重启远端的 hc server。"
	}
	return "检查连接后在首页重试。"
}

// Client speaks JSON-RPC 2.0 with a core, one message per line.
type Client struct {
	rw      io.ReadWriteCloser
	enc     *json.Encoder
	writeMu sync.Mutex
	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan reply
	events  func(Event)
	closed  chan struct{}
	once    sync.Once
	err     error
}

type reply struct {
	result json.RawMessage
	err    error
}

// NewClient starts reading from rw. It does not handshake; call Hello.
func NewClient(rw io.ReadWriteCloser) *Client {
	c := &Client{
		rw:      rw,
		enc:     json.NewEncoder(rw),
		pending: make(map[int64]chan reply),
		closed:  make(chan struct{}),
	}
	go c.read()
	return c
}

// OnEvent sets the handler called for every notification.
func (c *Client) OnEvent(f func(Event)) { c.events = f }

func (c *Client) read() {
	br := bufio.NewReaderSize(c.rw, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			c.dispatch(line)
		}
		if err != nil {
			c.fail(err)
			return
		}
	}
}

func (c *Client) dispatch(line []byte) {
	var env struct {
		ID     *int64          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error"`
	}
	if err := json.Unmarshal(line, &env); err != nil {
		return
	}
	if env.Method != "" {
		if env.Method == "event" && c.events != nil {
			var ev Event
			if json.Unmarshal(env.Params, &ev) == nil {
				c.events(ev)
			}
		}
		return // heartbeat and missed carry no work for this client
	}
	if env.ID == nil {
		return
	}
	c.mu.Lock()
	ch := c.pending[*env.ID]
	delete(c.pending, *env.ID)
	c.mu.Unlock()
	if ch == nil {
		return
	}
	if env.Error != nil {
		ch <- reply{err: env.Error}
		return
	}
	ch <- reply{result: env.Result}
}

func (c *Client) fail(err error) {
	c.once.Do(func() {
		c.err = err
		close(c.closed)
		c.mu.Lock()
		for id, ch := range c.pending {
			delete(c.pending, id)
			ch <- reply{err: fmt.Errorf("connection lost: %w", err)}
		}
		c.mu.Unlock()
	})
}

// Closed reports the connection's end, if any.
func (c *Client) Closed() <-chan struct{} { return c.closed }

// Close ends the connection.
func (c *Client) Close() error {
	c.fail(errors.New("closed"))
	return c.rw.Close()
}

// Call runs one method and decodes its result into out.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan reply, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	c.writeMu.Lock()
	err := c.enc.Encode(msg)
	c.writeMu.Unlock()
	if err != nil {
		c.fail(err)
		return err
	}

	select {
	case r := <-ch:
		if r.err != nil {
			if re, ok := r.err.(*RPCError); ok {
				re.Method = method
			}
			return r.err
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(r.result, out)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case <-c.closed:
		return errors.New("connection closed")
	}
}

// Hello is the handshake: protocol generation and what this core offers.
func (c *Client) Hello(ctx context.Context) (HelloResult, error) {
	var out HelloResult
	err := c.Call(ctx, "hello", map[string]any{
		"generation": 1,
		"calls_you":  "herdr-nsgo",
	}, &out)
	return out, err
}

// List returns the terminals and machines this core sees.
func (c *Client) List(ctx context.Context, all bool) (ListResult, error) {
	var out ListResult
	err := c.Call(ctx, "terminal.list", map[string]any{"all": all}, &out)
	return out, err
}

// Machines lists the connected and configured machines.
func (c *Client) Machines(ctx context.Context) ([]MachineView, error) {
	var out MachineListResult
	err := c.Call(ctx, "machine.list", nil, &out)
	return out.Machines, err
}

// Get returns one terminal's record.
func (c *Client) Get(ctx context.Context, addr string) (Terminal, error) {
	var out Terminal
	err := c.Call(ctx, "terminal.get", map[string]any{"terminal": addr}, &out)
	return out, err
}

// Read returns the screen plus lines of history as text.
func (c *Client) Read(ctx context.Context, addr string, lines int) (ReadResult, error) {
	var out ReadResult
	err := c.Call(ctx, "terminal.read", map[string]any{"terminal": addr, "lines": lines}, &out)
	return out, err
}

// Send pastes text and presses Enter, as a person would.
func (c *Client) Send(ctx context.Context, addr, text string) (SendResult, error) {
	var out SendResult
	err := c.Call(ctx, "terminal.send", map[string]any{"terminal": addr, "text": text}, &out)
	return out, err
}

// Keys presses keys by name: enter, esc, up, ctrl+c.
func (c *Client) Keys(ctx context.Context, addr string, keys ...string) (KeysResult, error) {
	var out KeysResult
	err := c.Call(ctx, "terminal.keys", map[string]any{"terminal": addr, "keys": keys}, &out)
	return out, err
}

// Interrupt presses the stop key and reports what happened.
func (c *Client) Interrupt(ctx context.Context, addr string) error {
	var out map[string]any
	return c.Call(ctx, "terminal.interrupt", map[string]any{"terminal": addr}, &out)
}

// Wait blocks until the program stops working, or the timeout is a check-in.
func (c *Client) Wait(ctx context.Context, addr string, timeoutMS int) (WaitResult, error) {
	var out WaitResult
	params := map[string]any{"terminal": addr}
	if timeoutMS > 0 {
		params["timeout"] = timeoutMS
	}
	err := c.Call(ctx, "terminal.wait", params, &out)
	return out, err
}

// Subscribe asks for the snapshot and then every fact change.
func (c *Client) Subscribe(ctx context.Context) (SubscribeResult, error) {
	var out SubscribeResult
	err := c.Call(ctx, "events.subscribe", map[string]any{}, &out)
	return out, err
}

// Ping reports whether the core answers within d.
func (c *Client) Ping(d time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var out map[string]any
	return c.Call(ctx, "server.status", nil, &out) == nil
}
