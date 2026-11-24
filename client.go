package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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
		return "那台机器没有连上。检查实例与机器列表，必要时重新添加或重连后再试。"
	case "finished":
		return "程序已经退出，不能再发送。要接着用就重新开一个终端。"
	case "blocked":
		return "程序正在等一个人操作。先处理当前审批或进终端查看，或按 Ctrl+C 打断后再发。"
	case "status_mismatch":
		return "程序状态和预期不一致，操作被拒绝。刷新这一屏再决定。"
	case "unavailable":
		return "核心暂时不可用（多半在重启）。等几秒自动重连即可。"
	case "config":
		return "核心的配置拒绝了这次请求。检查远端 ~/.config/herdr-core/config.toml。"
	case "needs_target":
		return "这条命令需要指定机器：用「机器/终端」的形式，例如 vps/t3。"
	case "needs_framed":
		return "该操作需要二进制帧复用连接，请重连核心后再试。"
	case "name_taken":
		return "这个会话名已被占用，换一个名称再启动。"
	case "spawn_failed":
		return "远端启动程序失败，确认命令或模板与目录存在后重试。"
	case "invalid_params":
		return "参数不对，这是客户端的问题；检查输入或查看诊断日志。"
	case "cancelled":
		return "操作被取消（可能有别人同时在操作这个终端）。重试一次。"
	}
	if e.Message != "" {
		return "检查连接后重试；仍失败就检查远端 hc server 状态。"
	}
	return "检查连接后重试。"
}

// Client speaks JSON-RPC 2.0 with a core: newline-delimited before hello,
// and optionally 9-byte LE binary framed after HelloFramed.
type Client struct {
	rw       io.ReadWriteCloser
	enc      *json.Encoder
	writeMu  sync.Mutex
	nextID   atomic.Int64
	framed   atomic.Bool
	helloID  atomic.Int64 // request ID expecting framed upgrade

	mu          sync.Mutex
	pending     map[int64]chan reply
	streams     map[uint32]*AttachStream
	eventChans  map[uint32]bool
	earlyFrames map[uint32][]queuedFrame
	events      func(Event)
	closed      chan struct{}
	once        sync.Once
	err         error
}

type reply struct {
	result json.RawMessage
	err    error
}

// NewClient starts reading from rw. It does not handshake; call Hello or HelloFramed.
func NewClient(rw io.ReadWriteCloser) *Client {
	c := &Client{
		rw:          rw,
		enc:         json.NewEncoder(rw),
		pending:     make(map[int64]chan reply),
		streams:     make(map[uint32]*AttachStream),
		eventChans:  make(map[uint32]bool),
		earlyFrames: make(map[uint32][]queuedFrame),
		closed:      make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// IsFramed reports whether this connection switched to binary framed mode.
func (c *Client) IsFramed() bool { return c.framed.Load() }

// OnEvent sets the handler called for every events.subscribe notification.
func (c *Client) OnEvent(f func(Event)) {
	c.mu.Lock()
	c.events = f
	c.mu.Unlock()
}

func (c *Client) readLoop() {
	br := bufio.NewReaderSize(c.rw, 1<<20)
	for !c.framed.Load() {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			upgraded := c.dispatchJSON(line, true)
			if upgraded {
				c.framed.Store(true)
				break
			}
		}
		if err != nil {
			c.fail(err)
			return
		}
	}
	// Binary framed loop after hello(framed: true).
	for {
		ch, kind, body, err := readRawFrame(br)
		if err != nil {
			c.fail(err)
			return
		}
		if ch == 0 && kind == FrameData {
			c.dispatchJSON(body, false)
			continue
		}
		c.dispatchStreamFrame(ch, kind, body)
	}
}

func (c *Client) dispatchStreamFrame(ch uint32, kind FrameKind, body []byte) {
	c.mu.Lock()
	isEventChan := c.eventChans[ch]
	stream := c.streams[ch]
	evFn := c.events
	if !isEventChan && stream == nil {
		// Frames on a freshly opened channel can arrive before Attach/Subscribe
		// finishes decoding its JSON-RPC reply; buffer them briefly so initial paint is never lost.
		if len(c.earlyFrames[ch]) < 64 {
			c.earlyFrames[ch] = append(c.earlyFrames[ch], queuedFrame{kind: kind, body: body})
		}
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	if isEventChan {
		if kind == FrameData && len(body) > 0 && evFn != nil {
			c.deliverEventJSON(body, evFn)
		}
		return
	}
	if stream != nil {
		stream.deliver(kind, body)
	}
}

func (c *Client) deliverEventJSON(raw []byte, evFn func(Event)) {
	// Framed subscribe channel may deliver raw Event JSON OR a JSON-RPC notification envelope.
	var env struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Type   string          `json:"type"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return
	}
	if env.Method == "event" && len(env.Params) > 0 {
		var ev Event
		if json.Unmarshal(env.Params, &ev) == nil {
			evFn(ev)
		}
		return
	}
	if env.Method == "missed" {
		evFn(Event{Type: "missed", Data: env.Params})
		return
	}
	if env.Type != "" {
		var ev Event
		if json.Unmarshal(raw, &ev) == nil {
			evFn(ev)
		}
	}
}

// dispatchJSON processes one JSON-RPC message and returns true if this message
// was a successful framed hello response that flips the wire to binary frames.
func (c *Client) dispatchJSON(line []byte, checkFramedUpgrade bool) bool {
	var env struct {
		ID     *int64          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error"`
	}
	if err := json.Unmarshal(line, &env); err != nil {
		return false
	}
	if env.Method != "" {
		c.mu.Lock()
		evFn := c.events
		c.mu.Unlock()
		if evFn != nil {
			if env.Method == "event" {
				var ev Event
				if json.Unmarshal(env.Params, &ev) == nil {
					evFn(ev)
				}
			} else if env.Method == "missed" {
				evFn(Event{Type: "missed", Data: env.Params})
			}
		}
		return false
	}
	if env.ID == nil {
		return false
	}

	upgraded := false
	if checkFramedUpgrade && env.Error == nil && *env.ID == c.helloID.Load() && *env.ID != 0 {
		var hr struct {
			Framed bool `json:"framed"`
		}
		if json.Unmarshal(env.Result, &hr) == nil && hr.Framed {
			upgraded = true
			c.framed.Store(true)
		}
	}

	c.mu.Lock()
	ch := c.pending[*env.ID]
	delete(c.pending, *env.ID)
	c.mu.Unlock()
	if ch == nil {
		return upgraded
	}
	if env.Error != nil {
		ch <- reply{err: env.Error}
		return upgraded
	}
	ch <- reply{result: env.Result}
	return upgraded
}

func (c *Client) writeFrame(ch uint32, kind FrameKind, body []byte) error {
	c.writeMu.Lock()
	err := writeRawFrame(c.rw, ch, kind, body)
	c.writeMu.Unlock()
	if err != nil {
		c.fail(err)
	}
	return err
}

func (c *Client) removeStream(ch uint32) {
	c.mu.Lock()
	delete(c.streams, ch)
	c.mu.Unlock()
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
		streams := make([]*AttachStream, 0, len(c.streams))
		for _, st := range c.streams {
			streams = append(streams, st)
		}
		c.streams = make(map[uint32]*AttachStream)
		c.mu.Unlock()
		for _, st := range streams {
			st.closeLocal(err)
		}
	})
}

// Closed reports the connection's end, if any.
func (c *Client) Closed() <-chan struct{} { return c.closed }

// Close ends the connection and all multiplexed streams.
func (c *Client) Close() error {
	c.fail(errors.New("closed"))
	return c.rw.Close()
}

// Call runs one JSON-RPC method and decodes its result into out.
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

	var err error
	if c.framed.Load() {
		var raw []byte
		raw, err = json.Marshal(msg)
		if err == nil {
			err = c.writeFrame(0, FrameData, raw)
		}
	} else {
		c.writeMu.Lock()
		err = c.enc.Encode(msg)
		c.writeMu.Unlock()
		if err != nil {
			c.fail(err)
		}
	}
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
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
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return errors.New("connection closed")
	}
}

// Hello is the unframed handshake (kept for compatibility with simple checks).
func (c *Client) Hello(ctx context.Context) (HelloResult, error) {
	var out HelloResult
	err := c.Call(ctx, "hello", map[string]any{
		"generation": 1,
		"calls_you":  "herdr-nsgo",
	}, &out)
	return out, err
}

// HelloFramed negotiates binary framed multiplexing for concurrent attach + events + RPC.
func (c *Client) HelloFramed(ctx context.Context) (HelloResult, error) {
	id := c.nextID.Add(1)
	c.helloID.Store(id)
	ch := make(chan reply, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "hello",
		"params": map[string]any{
			"generation": 1,
			"framed":     true,
			"calls_you":  "herdr-nsgo",
			"features":   []string{"resize_notifications", "automatic_input"},
		},
	}
	c.writeMu.Lock()
	err := c.enc.Encode(msg)
	c.writeMu.Unlock()
	if err != nil {
		c.fail(err)
		return HelloResult{}, err
	}

	select {
	case r := <-ch:
		if r.err != nil {
			if re, ok := r.err.(*RPCError); ok {
				re.Method = "hello"
			}
			return HelloResult{}, r.err
		}
		var out HelloResult
		if err := json.Unmarshal(r.result, &out); err != nil {
			return HelloResult{}, err
		}
		return out, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return HelloResult{}, ctx.Err()
	case <-c.closed:
		return HelloResult{}, errors.New("connection closed")
	}
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

// Templates lists the ready-made agent templates configured on this core.
func (c *Client) Templates(ctx context.Context) ([]Template, error) {
	var out TemplatesResult
	err := c.Call(ctx, "templates.list", nil, &out)
	return out.Templates, err
}

// Get returns one terminal's record.
func (c *Client) Get(ctx context.Context, addr string) (Terminal, error) {
	var out Terminal
	err := c.Call(ctx, "terminal.get", map[string]any{"terminal": addr}, &out)
	return out, err
}

// Run starts a new shell or agent/template terminal on this core (or remote machine via On).
func (c *Client) Run(ctx context.Context, p RunParams) (RunResult, error) {
	var out RunResult
	err := c.Call(ctx, "terminal.run", p, &out)
	return out, err
}

// Kill terminates a live terminal's process.
func (c *Client) Kill(ctx context.Context, addr string) (Terminal, error) {
	var out Terminal
	err := c.Call(ctx, "terminal.kill", map[string]any{"terminal": addr}, &out)
	return out, err
}

// Rm removes a finished terminal record.
func (c *Client) Rm(ctx context.Context, addr string) error {
	var out map[string]any
	return c.Call(ctx, "terminal.rm", map[string]any{"terminal": addr}, &out)
}

// Read returns the screen plus lines of history as text.
func (c *Client) Read(ctx context.Context, addr string, lines int) (ReadResult, error) {
	var out ReadResult
	err := c.Call(ctx, "terminal.read", map[string]any{"terminal": addr, "lines": lines}, &out)
	return out, err
}

// FileRead reads lines from a remote file on this core or a federated machine (p.On).
func (c *Client) FileRead(ctx context.Context, p FileReadParams) (FileReadResult, error) {
	var out FileReadResult
	err := c.Call(ctx, "file.read", p, &out)
	return out, err
}

// MachineExec executes a bounded command on this core or a federated machine (p.On).
// Timeout defaults to 5.0 seconds (capped at 600s per herdr-core OpenRPC schema).
func (c *Client) MachineExec(ctx context.Context, p ExecParams) (ExecResult, error) {
	if p.Timeout <= 0 {
		p.Timeout = 5.0
	} else if p.Timeout > 600 {
		p.Timeout = 600
	}
	var out ExecResult
	err := c.Call(ctx, "machine.exec", p, &out)
	return out, err
}

// Attach opens a real-time raw VT byte stream on a framed connection.
func (c *Client) Attach(ctx context.Context, p AttachParams) (*AttachStream, AttachResult, error) {
	if !c.framed.Load() {
		return nil, AttachResult{}, errors.New("terminal.attach requires HelloFramed first")
	}
	var res AttachResult
	if err := c.Call(ctx, "terminal.attach", p, &res); err != nil {
		return nil, AttachResult{}, err
	}
	stream := newAttachStream(c, res.Channel)
	c.mu.Lock()
	early := c.earlyFrames[res.Channel]
	delete(c.earlyFrames, res.Channel)
	for _, f := range early {
		stream.deliver(f.kind, f.body)
	}
	c.streams[res.Channel] = stream
	c.mu.Unlock()
	return stream, res, nil
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

// Subscribe asks for the snapshot and then every fact change, registering the
// returned framed event channel automatically when present.
func (c *Client) Subscribe(ctx context.Context) (SubscribeResult, error) {
	var out SubscribeResult
	err := c.Call(ctx, "events.subscribe", map[string]any{}, &out)
	if err == nil && out.Channel != nil {
		c.mu.Lock()
		c.eventChans[*out.Channel] = true
		early := c.earlyFrames[*out.Channel]
		delete(c.earlyFrames, *out.Channel)
		evFn := c.events
		c.mu.Unlock()
		if evFn != nil {
			for _, f := range early {
				if f.kind == FrameData && len(f.body) > 0 {
					c.deliverEventJSON(f.body, evFn)
				}
			}
		}
	}
	return out, err
}

// PutFile uploads bytes beside a live terminal using 64 KiB chunked RPCs when framed,
// or single-shot terminal.put on unframed links, returning the remote absolute path.
func (c *Client) PutFile(ctx context.Context, addr, name string, data []byte) (PutResult, error) {
	if !c.framed.Load() {
		var out PutResult
		err := c.Call(ctx, "terminal.put", map[string]any{
			"terminal": addr,
			"name":     name,
			"data":     base64.StdEncoding.EncodeToString(data),
		}, &out)
		return out, err
	}

	var rnd [16]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return PutResult{}, err
	}
	transfer := hex.EncodeToString(rnd[:])

	var beginOut struct {
		Transfer string `json:"transfer"`
	}
	if err := c.Call(ctx, "terminal.put_begin", map[string]any{
		"terminal": addr,
		"name":     name,
		"bytes":    len(data),
		"transfer": transfer,
	}, &beginOut); err != nil {
		return PutResult{}, err
	}
	if beginOut.Transfer != "" {
		transfer = beginOut.Transfer
	}

	const chunkSize = 64 * 1024
	for offset := 0; offset < len(data); offset += chunkSize {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		if err := c.Call(ctx, "terminal.put_chunk", map[string]any{
			"transfer": transfer,
			"offset":   offset,
			"data":     base64.StdEncoding.EncodeToString(data[offset:end]),
		}, nil); err != nil {
			abortCtx, abortCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = c.Call(abortCtx, "terminal.put_abort", map[string]any{"transfer": transfer}, nil)
			abortCancel()
			return PutResult{}, err
		}
	}

	var res PutResult
	if err := c.Call(ctx, "terminal.put_finish", map[string]any{"transfer": transfer}, &res); err != nil {
		return PutResult{}, err
	}
	return res, nil
}

// Ping reports whether the core answers within d (an RPCError still proves the link is alive).
func (c *Client) Ping(d time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var out map[string]any
	err := c.Call(ctx, "server.status", nil, &out)
	if err == nil {
		return true
	}
	var re *RPCError
	return errors.As(err, &re)
}
