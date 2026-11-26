package main

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Enter uses the agent's normal send RPC. Only alternate submit keys need a PTY write.
type chatSubmitMode string

const (
	chatNormal chatSubmitMode = ""
	chatSteer  chatSubmitMode = "插队"
	chatQueue  chatSubmitMode = "排队"
)

func defaultSubmitMode(kind string) chatSubmitMode {
	switch kind {
	case "claude":
		return chatQueue
	case "pi", "codex":
		return chatSteer
	}
	return chatNormal
}

// supportsModelEffortPicker reports whether a harness supports the Chat composer model & effort selector chips.
func supportsModelEffortPicker(kind string) bool {
	switch kind {
	case "claude", "codex", "pi", "omp", "antigravity":
		return true
	}
	return false
}

// agentSelectorCommand opens the agent-owned picker, which knows the account's models and limits.
func agentSelectorCommand(kind string, effort bool) string {
	switch kind {
	case "pi", "omp":
		if effort {
			return "/thinking"
		}
	case "claude", "antigravity":
		if effort {
			return "/effort"
		}
	case "codex": // Model selection leads into reasoning effort selection.
	default:
		return ""
	}
	return "/model"
}

func alternateSubmitKey(kind string, mode chatSubmitMode) string {
	switch {
	case kind == "pi" && mode == chatQueue:
		return "\x1b\r"
	case kind == "codex" && mode == chatQueue:
		return "\t"
	case kind == "claude" && mode == chatSteer:
		return "\x18\x13" // chat:sendNow, Ctrl+X Ctrl+S
	}
	return ""
}

func (c *Client) submitChat(ctx context.Context, addr, text, kind string, mode chatSubmitMode) error {
	key := alternateSubmitKey(kind, mode)
	if key == "" {
		_, err := c.Send(ctx, addr, text)
		return err
	}
	if strings.ContainsAny(text, "\x1b\x00") {
		return errors.New("消息包含终端控制字符，无法提交")
	}
	terminal, err := c.Get(ctx, addr)
	if err != nil {
		return err
	}
	if terminal.Cols <= 0 || terminal.Rows <= 0 {
		return errors.New("尚未取得终端尺寸，请稍后再试")
	}
	if terminal.NeedsYou() || terminal.Finished() || DetectHarnessKind(terminal) != kind {
		return errors.New("当前终端不接受此发送方式，请查看会话状态")
	}
	if kind == "codex" && mode == chatQueue && terminal.StateWord() != "working" {
		switch terminal.StateWord() {
		case "idle", "done":
			_, err := c.Send(ctx, addr, text)
			return err
		default:
			return errors.New("Codex 尚未报告工作状态，无法确认是否可以排队；草稿已保留。")
		}
	}
	// Observe attachments discard input. Reuse the current dimensions for this short-lived writer.
	stream, res, err := c.Attach(ctx, AttachParams{Terminal: addr, Resumed: true, Cols: terminal.Cols, Rows: terminal.Rows})
	if err != nil {
		return err
	}
	defer stream.Close()
	if DetectHarnessKind(res.Terminal) != kind || res.Terminal.NeedsYou() || res.Terminal.Finished() {
		return errors.New("当前终端不接受此发送方式，请查看会话状态")
	}
	if kind == "codex" && res.Terminal.StateWord() != "working" {
		return errors.New("Codex 工作状态已变化，草稿已保留。")
	}
	// One bracketed paste and one submit gesture, without resizing the desktop terminal.
	payload := []byte("\x1b[200~" + text + "\x1b[201~" + key)
	if res.AutomaticInput {
		return c.writeFrame(stream.Channel(), FrameAutomaticInput, payload)
	}
	_, err = stream.Write(payload)
	return err
}

// steerQueuedChat interrupts the currently running turn cleanly with Esc and nudges Enter
// so a previously queued message begins executing immediately without duplicating prompt text.
func (c *Client) steerQueuedChat(ctx context.Context, addr string) error {
	if _, err := c.Keys(ctx, addr, "esc"); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(250 * time.Millisecond):
	}
	_, err := c.Keys(ctx, addr, "enter")
	return err
}

type pendingChatReply struct {
	id, userID, phase string
	since             time.Time
}

func (c *ChatSessionController) confirmSubmission(id string, mode chatSubmitMode) {
	c.mu.Lock()
	changed := c.pendingReply.id == id && c.pendingReply.phase == "正在发送"
	if changed {
		switch mode {
		case chatQueue:
			c.pendingReply.phase = "已发送，等待处理"
		case chatSteer:
			c.pendingReply.phase = "已发送，正在插队"
		default:
			c.pendingReply.phase = "已提交，等待回复"
		}
	}
	c.mu.Unlock()
	if changed {
		c.bumpRev(false)
	}
}

// CanSteerItem reports whether a sent user message is still waiting in queue and can be promoted to steer now.
func (c *ChatSessionController) CanSteerItem(id string) bool {
	if c == nil || id == "" {
		return false
	}
	if c.rev != nil {
		_ = c.rev.Get()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pendingReply.id == "" || (c.pendingReply.id != id && c.pendingReply.userID != id) {
		return false
	}
	for _, opt := range c.optimistic {
		if opt.id == id {
			return opt.mode == string(chatQueue)
		}
	}
	return c.deliveryModes[id] == string(chatQueue)
}

// MarkSteered updates a waiting message locally to steered state once the user taps "插队".
func (c *ChatSessionController) MarkSteered(id string) {
	if c == nil || id == "" {
		return
	}
	c.mu.Lock()
	changed := false
	for i := range c.optimistic {
		if c.optimistic[i].id == id && c.optimistic[i].mode != string(chatSteer) {
			c.optimistic[i].mode = string(chatSteer)
			changed = true
		}
	}
	if c.deliveryModes == nil {
		c.deliveryModes = make(map[string]string)
	}
	if c.deliveryModes[id] != string(chatSteer) {
		c.deliveryModes[id] = string(chatSteer)
		changed = true
	}
	if c.pendingReply.id == id || c.pendingReply.userID == id {
		c.pendingReply.phase = "已插队，等待回复"
		changed = true
	}
	if changed {
		c.rebuildMergedLocked()
	}
	c.mu.Unlock()
	if changed {
		c.bumpRev(false)
	}
}

func (c *ChatSessionController) activity(t Terminal) string {
	if c.rev != nil {
		_ = c.rev.Get()
	}
	c.mu.Lock()
	phase := c.pendingReply.phase
	c.mu.Unlock()
	if phase == "正在发送" {
		return phase
	}
	if t.NeedsYou() || t.Finished() {
		return ""
	}
	if phase == "正在思考" || phase == "正在使用工具" {
		return phase
	}
	if t.StateWord() == "working" {
		msg := "正在生成回复"
		if t.Status != nil && t.Status.Msg != "" {
			msg = "正在处理 · " + firstLine(t.Status, t)
		}
		if phase != "" {
			return phase + " · " + msg
		}
		return msg
	}
	return phase
}
