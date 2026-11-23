package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// FrameKind identifies the 1-byte kind field in herdr-core's 9-byte LE frame header:
// <channel:uint32 LE><kind:uint8><length:uint32 LE><payload>.
type FrameKind uint8

const (
	FrameData           FrameKind = 0
	FrameCredit         FrameKind = 1
	FrameClose          FrameKind = 2
	FrameResize         FrameKind = 3
	FrameFocus          FrameKind = 4
	FrameFacts          FrameKind = 5
	FrameAutomaticInput FrameKind = 6
)

const frameHeaderSize = 9

// maxFramePayload bounds incoming framed payloads (16 MiB) against malformed streams.
const maxFramePayload = 16 << 20

type queuedFrame struct {
	kind FrameKind
	body []byte
}

// writeRawFrame writes one 9-byte LE header + payload onto w. Caller must hold writeMu.
func writeRawFrame(w io.Writer, ch uint32, kind FrameKind, body []byte) error {
	var hdr [frameHeaderSize]byte
	binary.LittleEndian.PutUint32(hdr[0:4], ch)
	hdr[4] = byte(kind)
	binary.LittleEndian.PutUint32(hdr[5:9], uint32(len(body)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(body) > 0 {
		if _, err := w.Write(body); err != nil {
			return err
		}
	}
	return nil
}

// readRawFrame reads one 9-byte LE header + payload from r.
func readRawFrame(r io.Reader) (ch uint32, kind FrameKind, body []byte, err error) {
	var hdr [frameHeaderSize]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, 0, nil, err
	}
	ch = binary.LittleEndian.Uint32(hdr[0:4])
	kind = FrameKind(hdr[4])
	ln := binary.LittleEndian.Uint32(hdr[5:9])
	if ln > maxFramePayload {
		return 0, 0, nil, fmt.Errorf("frame payload too large: %d", ln)
	}
	if ln > 0 {
		body = make([]byte, ln)
		if _, err = io.ReadFull(r, body); err != nil {
			return 0, 0, nil, err
		}
	}
	return ch, kind, body, nil
}

// AttachStream is a live bidirectional VT byte stream multiplexed over a framed Client.
// It uses a non-blocking queue between Client.readLoop and Read() so slow terminal
// rendering can never stall JSON-RPC control traffic on channel 0.
type AttachStream struct {
	client *Client
	ch     uint32

	mu       sync.Mutex
	cond     *sync.Cond
	buf      []byte
	readErr  error
	onResize func(cols, rows int)
	onFacts  func(Terminal)
	closed   bool
	once     sync.Once
}

func newAttachStream(c *Client, ch uint32) *AttachStream {
	s := &AttachStream{
		client: c,
		ch:     ch,
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Channel returns the herdr-core stream channel identifier.
func (s *AttachStream) Channel() uint32 { return s.ch }

// OnResize registers a callback invoked when the core notifies of a PTY size change.
func (s *AttachStream) OnResize(fn func(cols, rows int)) {
	s.mu.Lock()
	s.onResize = fn
	s.mu.Unlock()
}

// OnFacts registers a callback invoked when the core pushes terminal metadata updates.
func (s *AttachStream) OnFacts(fn func(Terminal)) {
	s.mu.Lock()
	s.onFacts = fn
	s.mu.Unlock()
}

// Read reads raw VT bytes from the attached terminal.
func (s *AttachStream) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.buf) == 0 && s.readErr == nil && !s.closed {
		s.cond.Wait()
	}
	if len(s.buf) > 0 {
		n := copy(p, s.buf)
		s.buf = s.buf[n:]
		if len(s.buf) == 0 {
			s.buf = nil
		}
		return n, nil
	}
	if s.readErr != nil {
		return 0, s.readErr
	}
	return 0, io.EOF
}

// Write sends raw terminal input bytes (keyboard / paste) as a DATA frame.
func (s *AttachStream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return 0, io.ErrClosedPipe
	}
	if err := s.client.writeFrame(s.ch, FrameData, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Resize sends a RESIZE frame (<cols:u16 LE><rows:u16 LE>) to update the remote PTY size.
func (s *AttachStream) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return errors.New("resize requires positive cols and rows")
	}
	if cols > 65535 {
		cols = 65535
	}
	if rows > 65535 {
		rows = 65535
	}
	var body [4]byte
	binary.LittleEndian.PutUint16(body[0:2], uint16(cols))
	binary.LittleEndian.PutUint16(body[2:4], uint16(rows))
	return s.client.writeFrame(s.ch, FrameResize, body[:])
}

// ResizePixels satisfies base/terminal's optional ResizePixels interface.
func (s *AttachStream) ResizePixels(cols, rows, _, _ int) error {
	return s.Resize(cols, rows)
}

// SendFocus sends a FOCUS frame indicating viewer focus state.
func (s *AttachStream) SendFocus(focused bool) error {
	b := byte(0)
	if focused {
		b = 1
	}
	return s.client.writeFrame(s.ch, FrameFocus, []byte{b})
}

// SendCredit grants flow-control credit bytes to the core for this channel.
func (s *AttachStream) SendCredit(bytes uint32) error {
	var body [4]byte
	binary.LittleEndian.PutUint32(body[:], bytes)
	return s.client.writeFrame(s.ch, FrameCredit, body[:])
}

// Close detaches this stream by sending a CLOSE frame and unblocking any reader.
func (s *AttachStream) Close() error {
	var err error
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		if s.readErr == nil {
			s.readErr = io.EOF
		}
		s.cond.Broadcast()
		s.mu.Unlock()
		s.client.removeStream(s.ch)
		err = s.client.writeFrame(s.ch, FrameClose, nil)
	})
	return err
}

const maxStreamBuffer = 4 << 20

func (s *AttachStream) deliver(kind FrameKind, body []byte) {
	switch kind {
	case FrameData:
		if len(body) == 0 {
			return
		}
		s.mu.Lock()
		if !s.closed {
			if len(s.buf)+len(body) > maxStreamBuffer {
				// Drop oldest bytes to preserve live responsiveness under heavy bursts.
				keep := maxStreamBuffer / 2
				if len(s.buf) > keep {
					s.buf = append([]byte(nil), s.buf[len(s.buf)-keep:]...)
				}
			}
			s.buf = append(s.buf, body...)
			s.cond.Broadcast()
		}
		s.mu.Unlock()
	case FrameResize:
		if len(body) >= 4 {
			cols := int(binary.LittleEndian.Uint16(body[0:2]))
			rows := int(binary.LittleEndian.Uint16(body[2:4]))
			s.mu.Lock()
			fn := s.onResize
			s.mu.Unlock()
			if fn != nil {
				fn(cols, rows)
			}
		}
	case FrameFacts:
		var term Terminal
		if json.Unmarshal(body, &term) == nil {
			s.mu.Lock()
			fn := s.onFacts
			s.mu.Unlock()
			if fn != nil {
				fn(term)
			}
		}
	case FrameClose:
		s.closeLocal(io.EOF)
	}
}

func (s *AttachStream) closeLocal(err error) {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		if err == nil {
			s.readErr = io.EOF
		} else {
			s.readErr = err
		}
		s.cond.Broadcast()
		s.mu.Unlock()
		s.client.removeStream(s.ch)
	})
}
