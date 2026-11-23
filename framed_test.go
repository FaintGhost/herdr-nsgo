package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"
)

// fakeFramedServer speaks the verified herdr-core 9-byte LE framed protocol over net.Pipe.
func fakeFramedServer(t *testing.T, srv net.Conn, onRPC func(method string, params json.RawMessage, writeFrame func(ch uint32, kind FrameKind, body []byte)) (any, error), onStreamFrame func(ch uint32, kind FrameKind, body []byte)) {
	t.Helper()
	go func() {
		defer srv.Close()
		// 1. Read newline-delimited hello line.
		var helloLine []byte
		buf := make([]byte, 1)
		for {
			n, err := srv.Read(buf)
			if err != nil {
				return
			}
			if n > 0 {
				helloLine = append(helloLine, buf[0])
				if buf[0] == '\n' {
					break
				}
			}
		}
		var helloReq struct {
			ID     int64 `json:"id"`
			Params struct {
				Framed bool `json:"framed"`
			} `json:"params"`
		}
		_ = json.Unmarshal(helloLine, &helloReq)
		helloResp, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      helloReq.ID,
			"result": map[string]any{
				"generation": 1,
				"framed":     helloReq.Params.Framed,
				"features":   []string{"framed", "attach", "events", "put_chunks"},
				"core":       map[string]any{"id": "core-test-1", "name": "test-core"},
			},
		})
		_, _ = srv.Write(append(helloResp, '\n'))

		writeFrame := func(ch uint32, kind FrameKind, body []byte) {
			var hdr [9]byte
			binary.LittleEndian.PutUint32(hdr[0:4], ch)
			hdr[4] = byte(kind)
			binary.LittleEndian.PutUint32(hdr[5:9], uint32(len(body)))
			_, _ = srv.Write(hdr[:])
			if len(body) > 0 {
				_, _ = srv.Write(body)
			}
		}

		// 2. Read framed requests.
		var hdr [9]byte
		for {
			if _, err := io.ReadFull(srv, hdr[:]); err != nil {
				return
			}
			ch := binary.LittleEndian.Uint32(hdr[0:4])
			kind := FrameKind(hdr[4])
			ln := binary.LittleEndian.Uint32(hdr[5:9])
			body := make([]byte, ln)
			if ln > 0 {
				if _, err := io.ReadFull(srv, body); err != nil {
					return
				}
			}
			if ch == 0 && kind == FrameData {
				var req struct {
					ID     int64           `json:"id"`
					Method string          `json:"method"`
					Params json.RawMessage `json:"params"`
				}
				if json.Unmarshal(body, &req) != nil {
					continue
				}
				res, err := onRPC(req.Method, req.Params, writeFrame)
				if err != nil {
					resp, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32000, "message": err.Error()}})
					writeFrame(0, FrameData, resp)
				} else {
					resp, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": res})
					writeFrame(0, FrameData, resp)
				}
				continue
			}
			if onStreamFrame != nil {
				onStreamFrame(ch, kind, body)
			}
		}
	}()
}

func TestFramedMuxRoundTripAndAttach(t *testing.T) {
	cliConn, srvConn := net.Pipe()
	defer cliConn.Close()

	gotInput := make(chan []byte, 4)
	gotResize := make(chan [2]uint16, 4)
	gotClose := make(chan uint32, 4)

	fakeFramedServer(t, srvConn, func(method string, params json.RawMessage, writeFrame func(uint32, FrameKind, []byte)) (any, error) {
		switch method {
		case "terminal.attach":
			go func() {
				time.Sleep(10 * time.Millisecond)
				var sz [4]byte
				binary.LittleEndian.PutUint16(sz[0:2], 80)
				binary.LittleEndian.PutUint16(sz[2:4], 24)
				writeFrame(1, FrameResize, sz[:])
				writeFrame(1, FrameData, []byte("\x1b[32mhello-vt\x1b[0m"))
			}()
			return AttachResult{
				Channel:             1,
				ResizeNotifications: true,
				AutomaticInput:      true,
				Terminal:            Terminal{Address: "t1", Name: "dev", Cols: 80, Rows: 24},
			}, nil
		case "events.subscribe":
			ch := uint32(2)
			go func() {
				time.Sleep(15 * time.Millisecond)
				ev, _ := json.Marshal(Event{Type: "status", Terminal: "t1", Seq: 2})
				writeFrame(ch, FrameData, ev)
			}()
			return SubscribeResult{
				Channel:   &ch,
				Seq:       1,
				Terminals: []Terminal{{Address: "t1", Name: "dev"}},
			}, nil
		}
		return map[string]any{}, nil
	}, func(ch uint32, kind FrameKind, body []byte) {
		switch kind {
		case FrameData:
			gotInput <- append([]byte(nil), body...)
		case FrameResize:
			if len(body) == 4 {
				gotResize <- [2]uint16{binary.LittleEndian.Uint16(body[0:2]), binary.LittleEndian.Uint16(body[2:4])}
			}
		case FrameClose:
			gotClose <- ch
		}
	})

	c := NewClient(cliConn)
	defer c.Close()

	evCh := make(chan Event, 4)
	c.OnEvent(func(ev Event) { evCh <- ev })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hello, err := c.HelloFramed(ctx)
	if err != nil {
		t.Fatalf("HelloFramed: %v", err)
	}
	if !hello.Framed || hello.Core.ID != "core-test-1" {
		t.Fatalf("unexpected hello: %+v", hello)
	}

	sub, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if len(sub.Terminals) != 1 {
		t.Fatalf("want 1 terminal in snapshot, got %d", len(sub.Terminals))
	}

	stream, _, err := c.Attach(ctx, AttachParams{
		Terminal:            "t1",
		Cols:                58,
		Rows:                32,
		History:             100,
		Observe:             true,
		ResizeNotifications: true,
	})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}

	resizedCh := make(chan [2]int, 2)
	stream.OnResize(func(cols, rows int) { resizedCh <- [2]int{cols, rows} })

	readBuf := make([]byte, 64)
	n, err := stream.Read(readBuf)
	if err != nil {
		t.Fatalf("stream.Read: %v", err)
	}
	if string(readBuf[:n]) != "\x1b[32mhello-vt\x1b[0m" {
		t.Fatalf("got VT %q", string(readBuf[:n]))
	}

	if _, err := stream.Write([]byte("ls\r")); err != nil {
		t.Fatalf("stream.Write: %v", err)
	}
	if err := stream.Resize(60, 30); err != nil {
		t.Fatalf("stream.Resize: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close: %v", err)
	}

	select {
	case in := <-gotInput:
		if string(in) != "ls\r" {
			t.Errorf("server got input %q, want ls\\r", in)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for input frame")
	}
	select {
	case sz := <-gotResize:
		if sz != [2]uint16{60, 30} {
			t.Errorf("server got resize %v, want [60 30]", sz)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for resize frame")
	}
	select {
	case ch := <-gotClose:
		if ch != 1 {
			t.Errorf("server got close on ch %d, want 1", ch)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for close frame")
	}
	select {
	case ev := <-evCh:
		if ev.Type != "status" || ev.Terminal != "t1" {
			t.Errorf("unexpected event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for subscribed framed event")
	}
}

func TestPutFileChunked(t *testing.T) {
	cliConn, srvConn := net.Pipe()
	defer cliConn.Close()

	var received bytes.Buffer
	var chunks int
	var transferID string

	fakeFramedServer(t, srvConn, func(method string, params json.RawMessage, _ func(uint32, FrameKind, []byte)) (any, error) {
		switch method {
		case "terminal.put_begin":
			var p struct {
				Terminal string `json:"terminal"`
				Name     string `json:"name"`
				Bytes    int    `json:"bytes"`
				Transfer string `json:"transfer"`
			}
			_ = json.Unmarshal(params, &p)
			transferID = p.Transfer
			if len(transferID) < 16 || len(transferID) > 64 {
				t.Errorf("invalid transfer id len: %d", len(transferID))
			}
			return map[string]any{"transfer": transferID}, nil
		case "terminal.put_chunk":
			var p struct {
				Transfer string `json:"transfer"`
				Offset   int    `json:"offset"`
				Data     string `json:"data"`
			}
			_ = json.Unmarshal(params, &p)
			if p.Offset != received.Len() {
				t.Errorf("chunk offset = %d, want %d", p.Offset, received.Len())
			}
			raw, err := base64.StdEncoding.DecodeString(p.Data)
			if err != nil {
				t.Errorf("bad base64: %v", err)
			}
			received.Write(raw)
			chunks++
			return map[string]any{"received": received.Len()}, nil
		case "terminal.put_finish":
			return PutResult{Path: "/tmp/herdr-put/shot.png", Bytes: received.Len()}, nil
		}
		return map[string]any{}, nil
	}, nil)

	c := NewClient(cliConn)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.HelloFramed(ctx); err != nil {
		t.Fatalf("HelloFramed: %v", err)
	}

	payload := bytes.Repeat([]byte("ABCD"), 40*1024) // 160 KiB -> 3 chunks (64 + 64 + 32)
	res, err := c.PutFile(ctx, "t1", "shot.png", payload)
	if err != nil {
		t.Fatalf("PutFile: %v", err)
	}
	if chunks != 3 {
		t.Errorf("chunks = %d, want 3", chunks)
	}
	if !bytes.Equal(received.Bytes(), payload) {
		t.Errorf("reassembled payload mismatch: got %d bytes, want %d", received.Len(), len(payload))
	}
	if res.Path != "/tmp/herdr-put/shot.png" || res.Bytes != len(payload) {
		t.Errorf("unexpected PutResult: %+v", res)
	}
}
