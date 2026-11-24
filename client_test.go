package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestClientFileReadMachineExecAndSplitAddr(t *testing.T) {
	if on, local := SplitMachineAddr("kr/t12"); on != "kr" || local != "t12" {
		t.Fatalf("SplitMachineAddr(kr/t12) = (%q, %q), want (kr, t12)", on, local)
	}
	if on, local := SplitMachineAddr("t9"); on != "" || local != "t9" {
		t.Fatalf("SplitMachineAddr(t9) = (%q, %q), want ('', t9)", on, local)
	}

	// Verify Terminal.Argv, Terminal.PID, Foreground.Argv JSON unmarshaling.
	var term Terminal
	rawTerm := `{"address":"kr/t12","argv":["sh","-c","omp -r abc"],"pid":1872804,"cwd":"/root","state":"live","created_ms":1,"cols":80,"rows":24,"labels":{},"foreground":{"pid":1872804,"name":"sh","cwd":"/root","argv":["sh","-c","omp -r abc"]}}`
	if err := json.Unmarshal([]byte(rawTerm), &term); err != nil {
		t.Fatalf("unmarshal terminal: %v", err)
	}
	if len(term.Argv) != 3 || term.PID == nil || *term.PID != 1872804 || term.Foreground == nil || len(term.Foreground.Argv) != 3 {
		t.Fatalf("unexpected Terminal/Foreground argv or pid: %+v / %+v", term, term.Foreground)
	}

	// Verify FileRead & MachineExec JSON-RPC wire contract over a pipe server.
	cConn, sConn := net.Pipe()
	defer cConn.Close()
	defer sConn.Close()

	go func() {
		br := bufio.NewReader(sConn)
		enc := json.NewEncoder(sConn)
		for i := 0; i < 3; i++ {
			line, err := br.ReadBytes('\n')
			if err != nil {
				return
			}
			var req struct {
				ID     int64                  `json:"id"`
				Method string                 `json:"method"`
				Params map[string]interface{} `json:"params"`
			}
			if err := json.Unmarshal(line, &req); err != nil {
				return
			}
			switch req.Method {
			case "file.read":
				off, _ := req.Params["offset"].(float64)
				if off > 10 {
					_ = enc.Encode(map[string]any{
						"jsonrpc": "2.0",
						"id":      req.ID,
						"error": map[string]any{
							"code":    -32602,
							"message": "offset 50: the file has 10 lines",
							"data":    map[string]any{"kind": "invalid_params"},
						},
					})
					continue
				}
				if req.Params["on"] != "kr" || req.Params["path"] != "/root/.omp/test.jsonl" {
					_ = enc.Encode(map[string]any{
						"jsonrpc": "2.0",
						"id":      req.ID,
						"error":   map[string]any{"code": -32602, "message": "bad params"},
					})
					continue
				}
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"text":  "{\"type\":\"session\"}\n",
						"first": 1,
						"last":  1,
						"lines": 10,
					},
				})
			case "machine.exec":
				to, _ := req.Params["timeout"].(float64)
				// Must be seconds (default 5.0), NEVER milliseconds (>600 rejected by herdr-core)!
				if to <= 0 || to > 600 {
					_ = enc.Encode(map[string]any{
						"jsonrpc": "2.0",
						"id":      req.ID,
						"error":   map[string]any{"code": -32602, "message": "bad timeout seconds"},
					})
					continue
				}
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"code":      0,
						"stdout":    base64.StdEncoding.EncodeToString([]byte("/root/.omp/found.jsonl\n")),
						"stderr":    base64.StdEncoding.EncodeToString([]byte("")),
						"ms":        12,
						"timed_out": false,
						"truncated": false,
					},
				})
			}
		}
	}()

	client := NewClient(cConn)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fr, err := client.FileRead(ctx, FileReadParams{
		Path:   "/root/.omp/test.jsonl",
		Offset: 1,
		Limit:  5,
		On:     "kr",
	})
	if err != nil {
		t.Fatalf("FileRead failed: %v", err)
	}
	if fr.First != 1 || fr.Last != 1 || fr.Lines != 10 || !strings.Contains(fr.Text, `"session"`) {
		t.Fatalf("unexpected FileReadResult: %+v", fr)
	}

	_, err = client.FileRead(ctx, FileReadParams{
		Path:   "/root/.omp/test.jsonl",
		Offset: 50,
		Limit:  5,
		On:     "kr",
	})
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Kind() != "invalid_params" {
		t.Fatalf("expected invalid_params RPCError on offset>lines, got %v", err)
	}

	er, err := client.MachineExec(ctx, ExecParams{
		Line: "ls /root/.omp/*.jsonl",
		On:   "kr",
	})
	if err != nil {
		t.Fatalf("MachineExec failed: %v", err)
	}
	if er.Code == nil || *er.Code != 0 || strings.TrimSpace(er.StdoutText()) != "/root/.omp/found.jsonl" {
		t.Fatalf("unexpected ExecResult: %+v stdout=%q", er, er.StdoutText())
	}
}

// TestClientAgainstLocalCore speaks the real protocol with the hc on this
// machine, so the wire format is checked against a core, not a stub.
func TestClientAgainstLocalCore(t *testing.T) {
	if _, err := exec.LookPath("hc"); err != nil {
		t.Skip("hc is not on PATH")
	}
	rw, err := dialLocal()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c := NewClient(rw)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	hello, err := c.Hello(ctx)
	if err != nil {
		t.Fatalf("hello: %v", err)
	}
	if hello.Generation != 1 {
		t.Errorf("generation = %d, want 1", hello.Generation)
	}
	if hello.Core.Name == "" {
		t.Error("core name is empty")
	}

	list, err := c.List(ctx, false)
	if err != nil {
		t.Fatalf("terminal.list: %v", err)
	}
	t.Logf("core %q, %d terminals, %d machines", hello.Core.Name, len(list.Terminals), len(list.Machines))

	for _, term := range list.Terminals {
		if term.Address == "" {
			t.Fatal("a terminal has no address")
		}
		if term.StateWord() == "" {
			t.Errorf("%s has no state word", term.Address)
		}
	}

	if len(list.Terminals) > 0 {
		first := list.Terminals[0]
		res, err := c.Read(ctx, first.Address, 5)
		if err != nil {
			t.Fatalf("terminal.read %s: %v", first.Address, err)
		}
		if res.How == "" {
			t.Error("read result has no how")
		}
		t.Logf("read %s: how=%s complete=%v bytes=%d", first.Address, res.How, res.Complete, len(res.Text))
	}
}

// TestFramedAttachAgainstLocalCore verifies HelloFramed, Subscribe, Templates,
// and observe-mode AttachStream against the real local hc core.
func TestFramedAttachAgainstLocalCore(t *testing.T) {
	if _, err := exec.LookPath("hc"); err != nil {
		t.Skip("hc is not on PATH")
	}
	rw, err := dialLocal()
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c := NewClient(rw)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	hello, err := c.HelloFramed(ctx)
	if err != nil {
		t.Fatalf("HelloFramed: %v", err)
	}
	if !hello.Framed || !c.IsFramed() {
		t.Fatalf("expected framed mode, got %+v", hello)
	}

	sub, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if _, err := c.Templates(ctx); err != nil {
		t.Fatalf("Templates: %v", err)
	}
	var liveAddr string
	for _, term := range sub.Terminals {
		if !term.Finished() {
			liveAddr = term.Address
			break
		}
	}
	if liveAddr == "" {
		t.Skip("no live terminals on local core to attach")
	}

	stream, res, err := c.Attach(ctx, AttachParams{
		Terminal:            liveAddr,
		Cols:                60,
		Rows:                24,
		History:             20,
		Observe:             true,
		ResizeNotifications: true,
	})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer stream.Close()
	if res.Channel == 0 {
		t.Fatalf("expected nonzero attach channel")
	}
	buf := make([]byte, 4096)
	readDone := make(chan int, 1)
	go func() {
		n, _ := stream.Read(buf)
		readDone <- n
	}()
	select {
	case n := <-readDone:
		if n == 0 {
			t.Error("expected initial paint bytes from attach")
		}
		t.Logf("attached ch=%d terminal=%s received %d initial VT bytes", res.Channel, sub.Terminals[0].Address, n)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for initial paint from live hc terminal.attach")
	}
}

// TestFailureCarriesRemediation checks the rule the UI depends on: a failure
// names what to do next, never only what went wrong.
func TestFailureCarriesRemediation(t *testing.T) {
	for _, kind := range []string{"not_found", "machine_unknown", "finished", "blocked", "unavailable"} {
		e := &RPCError{Message: "x"}
		e.Data.Kind = kind
		got := e.Remediation()
		if got == "" || got == "x" {
			t.Errorf("%s: remediation %q does not name a repair", kind, got)
		}
	}
}

// TestExplainKeepsObservedFailure makes sure a dial failure is not flattened
// into a generic message.
func TestExplainKeepsObservedFailure(t *testing.T) {
	cases := map[string]string{
		"dial tcp: lookup vps: no such host":        "域名解析失败",
		"dial tcp 1.2.3.4:22: connect: connection refused": "对方拒绝了连接",
		"ssh: handshake failed: unable to authenticate":    "SSH 认证失败",
	}
	for in, want := range cases {
		if got := explain(errString(in)); !strings.Contains(got, want) {
			t.Errorf("explain(%q) = %q, want it to mention %q", in, got, want)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// TestSSHHost exercises the exact path the phone takes: SSH to a real host,
// run `hc connect` there, and list its terminals.
func TestSSHHost(t *testing.T) {
	target := os.Getenv("HERDR_TEST_SSH")
	if target == "" {
		t.Skip("set HERDR_TEST_SSH=user@host[:port] to run")
	}
	keyPath := os.Getenv("HERDR_TEST_KEY")
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	user, host, _ := strings.Cut(target, "@")
	h := Host{Label: host, Target: host, User: user, Key: string(key)}

	rw, err := Dial(h)
	if err != nil {
		t.Fatalf("dial %s: %v", target, err)
	}
	c := NewClient(rw)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	hello, err := c.Hello(ctx)
	if err != nil {
		t.Fatalf("hello: %v", err)
	}
	list, err := c.List(ctx, false)
	if err != nil {
		t.Fatalf("terminal.list: %v", err)
	}
	t.Logf("core %q: %d terminals, %d machines", hello.Core.Name, len(list.Terminals), len(list.Machines))
	if len(list.Terminals) == 0 {
		t.Error("no terminals: the app would show an empty list")
	}
}
