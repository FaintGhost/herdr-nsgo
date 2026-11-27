package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// PromptKind distinguishes permission dialogs, interactive questions, and multi-question submit review tabs.
type PromptKind string

const (
	PromptKindPermission PromptKind = "permission"
	PromptKindQuestion   PromptKind = "question"
	PromptKindReview     PromptKind = "review"
)

// OptionTone drives semantic styling in Quiet Studio cards without raw colors.
type OptionTone string

const (
	TonePrimary OptionTone = "primary"
	ToneNeutral OptionTone = "neutral"
	ToneDanger  OptionTone = "danger"
)

// PromptOption is one selectable row parsed live from a blocked harness TUI.
type PromptOption struct {
	Index          int
	KeyHint        string
	Label          string
	Description    string
	Tone           OptionTone
	Checked        bool
	NeedsText      bool
	InlineTextMode bool
	IsSubmit       bool
}

// InteractivePrompt is the normalized live TUI permission or question dialog ready for UI rendering and safe driving.
type InteractivePrompt struct {
	Kind         PromptKind
	Harness      string
	Title        string
	Detail       string
	Cursor       int
	Options      []PromptOption
	MultiSelect  bool
	Tabs         []string
	ActiveTab    int
	CanSwitchTab bool
	Fingerprint  string
}

// PendingQuestion holds structured metadata extracted from an unanswered AskUserQuestion / ask_question tool call.
type PendingQuestion struct {
	ToolID    string
	ToolName  string
	Questions []PendingQuestionItem
}

type PendingQuestionItem struct {
	Question    string
	Header      string
	MultiSelect bool
	Options     []PendingQuestionOption
}

type PendingQuestionOption struct {
	Label       string
	Description string
}

var (
	rePromptNumbered = regexp.MustCompile(`^(\d+)[.)]\s+(.+)$`)
	rePromptCheckbox = regexp.MustCompile(`^(\[[ xX✓✔]\]|[☐☑☒])\s*(.*)$`)
	reBracketTab     = regexp.MustCompile(`\[([^\[\]]+)\]`)
)

// ParseInteractivePrompt parses a live visible terminal screen (from terminal.read lines=0)
// into a structured InteractivePrompt. It fails closed (returns ok=false) whenever fewer than
// 2 valid options or no recognizable cursor/dialog structure is found.
func ParseInteractivePrompt(screen, harness, statusKind string) (InteractivePrompt, bool) {
	norm := strings.ReplaceAll(screen, "\r\n", "\n")
	rawLines := strings.Split(norm, "\n")
	if len(rawLines) == 0 {
		return InteractivePrompt{}, false
	}

	lowScreen := strings.ToLower(norm)

	var (
		opts         []PromptOption
		cursor       = -1
		firstOptLine = -1
		multiSelect  bool
		headerLines  []string
		tabs         []string
		activeTab    = -1
		canSwitchTab bool
	)

	resetBlock := func() {
		opts = nil
		cursor = -1
		firstOptLine = -1
		multiSelect = false
		headerLines = nil
		tabs = nil
		activeTab = -1
	}

	if strings.Contains(lowScreen, "tab to switch") || strings.Contains(lowScreen, "←/→") {
		canSwitchTab = true
	}

	// Scan top-to-bottom, resetting whenever a dialog rule or new prose header starts
	// after an earlier list so scrollback markdown lists/prompts never bleed into the bottom dialog.
	for i, raw := range rawLines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || isPromptFooterHint(trimmed) {
			continue
		}
		if isTUIStatusChrome(trimmed) {
			// A horizontal rule before the bottom dialog separates scrollback from the active dialog;
			// ignore a trailing bottom border rule if no non-footer content follows.
			if hasMeaningfulLinesAfter(rawLines, i+1) {
				resetBlock()
			}
			continue
		}

		if tList, act := parseTabStripLine(trimmed); len(tList) >= 2 && act >= 0 {
			if len(opts) > 0 {
				resetBlock()
			}
			tabs = tList
			activeTab = act
			canSwitchTab = true
			continue
		}

		active := false
		body := trimmed
		for _, prefix := range []string{"❯", "›", "→", "▸", ">"} {
			if strings.HasPrefix(body, prefix+" ") || body == prefix {
				active = true
				body = strings.TrimSpace(strings.TrimPrefix(body, prefix))
				break
			}
		}

		keyHint := ""
		if m := rePromptNumbered.FindStringSubmatch(body); m != nil {
			keyHint = m[1]
			body = strings.TrimSpace(m[2])
		}

		checked := false
		hasCheckbox := false
		if m := rePromptCheckbox.FindStringSubmatch(body); m != nil {
			hasCheckbox = true
			multiSelect = true
			mark := m[1]
			checked = strings.ContainsAny(mark, "xX✓✔☑☒")
			body = strings.TrimSpace(m[2])
		}

		isOptionLine := keyHint != "" || hasCheckbox
		if !isOptionLine && firstOptLine >= 0 {
			if isUnnumberedSentinelOption(body) {
				isOptionLine = true
			}
		}
		if !isOptionLine && (isSubmitLabel(body) || (active && isDenyOrCancelLabel(body))) {
			isOptionLine = true
		}

		if isOptionLine && body != "" {
			if firstOptLine < 0 {
				firstOptLine = i
			}
			idx := len(opts)
			if active && cursor < 0 {
				cursor = idx
			}
			needsText, inlineText := classifyNeedsText(body, harness)
			isSubmit := isSubmitLabel(body)
			opts = append(opts, PromptOption{
				Index:          idx,
				KeyHint:        keyHint,
				Label:          body,
				Checked:        checked,
				NeedsText:      needsText,
				InlineTextMode: inlineText,
				IsSubmit:       isSubmit,
			})
			continue
		}

		// Indented description line immediately under an option row (before footer).
		if firstOptLine >= 0 && len(opts) > 0 && !active {
			leadingSpaces := len(raw) - len(strings.TrimLeft(raw, " \t"))
			if leadingSpaces >= 3 {
				last := &opts[len(opts)-1]
				if last.Description == "" {
					last.Description = trimmed
				} else {
					last.Description += " " + trimmed
				}
				continue
			}
		}

		// Non-option, non-indented prose line arriving after an earlier option list
		// means that earlier list was scrollback prose; begin a fresh dialog candidate.
		if len(opts) > 0 {
			resetBlock()
		}
		headerLines = append(headerLines, trimmed)
	}

	if len(opts) < 2 {
		return InteractivePrompt{}, false
	}
	if cursor < 0 {
		// Require an explicit cursor marker for safety unless this is a verified numbered dialog with clear footer cue.
		if !hasDialogFooterCue(lowScreen) {
			return InteractivePrompt{}, false
		}
		cursor = 0
	}

	hasSubmitOpt := false
	hasCancelOpt := false
	for _, o := range opts {
		if o.IsSubmit {
			hasSubmitOpt = true
		}
		if isDenyOrCancelLabel(o.Label) {
			hasCancelOpt = true
		}
	}
	lowHeader := strings.ToLower(strings.Join(headerLines, "\n"))
	isReview := !multiSelect && (strings.Contains(lowHeader, "review your answers") || (hasSubmitOpt && hasCancelOpt))

	kind := PromptKindPermission
	if isReview {
		kind = PromptKindReview
	} else if statusKind == "question" || multiSelect || canSwitchTab || hasQuestionHeader(headerLines) {
		kind = PromptKindQuestion
	}

	// Assign semantic tones & default key hints.
	for i := range opts {
		if opts[i].KeyHint == "" && !opts[i].IsSubmit && kind != PromptKindReview {
			opts[i].KeyHint = fmt.Sprintf("%d", i+1)
		}
		opts[i].Tone = classifyOptionTone(opts[i], i, kind, harness)
	}

	title, detail := extractPromptTitleAndDetail(headerLines, kind)
	p := InteractivePrompt{
		Kind:         kind,
		Harness:      harness,
		Title:        title,
		Detail:       detail,
		Cursor:       cursor,
		Options:      opts,
		MultiSelect:  multiSelect,
		Tabs:         tabs,
		ActiveTab:    activeTab,
		CanSwitchTab: canSwitchTab,
	}
	p.Fingerprint = computePromptFingerprint(p)
	return p, true
}

func hasMeaningfulLinesAfter(lines []string, start int) bool {
	for i := start; i < len(lines); i++ {
		tr := strings.TrimSpace(lines[i])
		if tr == "" || isTUIStatusChrome(tr) || isPromptFooterHint(tr) {
			continue
		}
		return true
	}
	return false
}

func parseTabStripLine(trimmed string) ([]string, int) {
	if !strings.Contains(trimmed, "[") || !strings.Contains(trimmed, "]") {
		return nil, -1
	}
	// Avoid misclassifying checkbox lines like "[x] Unit tests".
	if rePromptCheckbox.MatchString(trimmed) || rePromptNumbered.MatchString(trimmed) {
		return nil, -1
	}
	parts := regexp.MustCompile(`\s{2,}`).Split(trimmed, -1)
	if len(parts) < 2 || len(parts) > 6 {
		return nil, -1
	}
	var tabs []string
	active := -1
	for i, part := range parts {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if m := reBracketTab.FindStringSubmatch(p); m != nil && strings.HasPrefix(p, "[") && strings.HasSuffix(p, "]") {
			active = len(tabs)
			tabs = append(tabs, strings.TrimSpace(m[1]))
		} else {
			tabs = append(tabs, strings.Trim(p, "✓✔ "))
		}
		_ = i
	}
	if len(tabs) < 2 || active < 0 {
		return nil, -1
	}
	return tabs, active
}

func isTabStripCandidate(trimmed string) bool {
	tabs, act := parseTabStripLine(trimmed)
	return len(tabs) >= 2 && act >= 0
}

func isPromptFooterHint(trimmed string) bool {
	low := strings.ToLower(trimmed)
	for _, kw := range []string{
		"to navigate", "to select", "to cancel", "to toggle",
		"to switch", "to confirm", "press enter", "esc to",
		"space to toggle", "tab to switch", "n to add a note",
	} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

func hasDialogFooterCue(lowScreen string) bool {
	for _, kw := range []string{"enter to select", "enter to confirm", "esc to cancel", "to navigate", "do you want to proceed"} {
		if strings.Contains(lowScreen, kw) {
			return true
		}
	}
	return false
}

func isUnnumberedSentinelOption(body string) bool {
	low := strings.ToLower(strings.TrimSpace(body))
	switch low {
	case "next", "submit", "submit answers", "cancel", "other", "other...", "type something", "type something.":
		return true
	}
	return strings.HasPrefix(low, "submit ") || strings.HasPrefix(low, "type something")
}

func isSubmitLabel(body string) bool {
	low := strings.ToLower(strings.TrimSpace(body))
	return low == "next" || low == "submit" || low == "submit answers" || strings.HasPrefix(low, "submit answers")
}

func isDenyOrCancelLabel(body string) bool {
	low := strings.ToLower(strings.TrimSpace(body))
	if low == "no" || low == "deny" || low == "cancel" || low == "reject" || low == "decline" || low == "拒绝" || low == "取消" {
		return true
	}
	for _, prefix := range []string{"no,", "no ", "deny ", "reject ", "decline ", "cancel ", "拒绝"} {
		if strings.HasPrefix(low, prefix) {
			return true
		}
	}
	return false
}

func classifyNeedsText(body, harness string) (needsText, inlineText bool) {
	low := strings.ToLower(strings.TrimSpace(body))
	switch {
	case strings.Contains(low, "tell claude what to do"),
		strings.Contains(low, "type something"),
		strings.Contains(low, "type your own"),
		low == "other",
		low == "other...",
		strings.HasPrefix(low, "other:") || strings.HasPrefix(low, "other (") || strings.HasPrefix(low, "其他"):
		inline := harness == "claude" || harness == "pi" || harness == "omp" || strings.Contains(low, "type something") || strings.Contains(low, "tell claude")
		return true, inline
	}
	return false, false
}

func classifyOptionTone(opt PromptOption, idx int, kind PromptKind, _ string) OptionTone {
	if isDenyOrCancelLabel(opt.Label) {
		return ToneDanger
	}
	if kind == PromptKindReview {
		if opt.IsSubmit || idx == 0 {
			return TonePrimary
		}
		return ToneNeutral
	}
	if kind == PromptKindPermission {
		if idx == 0 {
			return TonePrimary
		}
		return ToneNeutral
	}
	if opt.IsSubmit {
		return TonePrimary
	}
	return ToneNeutral
}

func hasQuestionHeader(lines []string) bool {
	for _, ln := range lines {
		if strings.HasSuffix(ln, "?") || strings.HasSuffix(ln, "？") {
			low := strings.ToLower(ln)
			if !strings.Contains(low, "do you want to proceed") && !strings.Contains(low, "allow ") {
				return true
			}
		}
	}
	return false
}

func extractPromptTitleAndDetail(headerLines []string, kind PromptKind) (title, detail string) {
	if len(headerLines) == 0 {
		switch kind {
		case PromptKindReview:
			return "确认提交回答", ""
		case PromptKindQuestion:
			return "等待回答", ""
		default:
			return "请求执行权限", ""
		}
	}
	if kind == PromptKindQuestion {
		// Prefer the last question-like line as the prompt title.
		for i := len(headerLines) - 1; i >= 0; i-- {
			if strings.HasSuffix(headerLines[i], "?") || strings.HasSuffix(headerLines[i], "？") {
				title = headerLines[i]
				if i > 0 {
					detail = strings.Join(headerLines[:i], "\n")
				}
				return title, detail
			}
		}
		return headerLines[len(headerLines)-1], strings.Join(headerLines[:len(headerLines)-1], "\n")
	}
	title = headerLines[0]
	if len(headerLines) > 1 {
		detail = strings.Join(headerLines[1:], "\n")
	}
	return title, detail
}

func computePromptFingerprint(p InteractivePrompt) string {
	var b strings.Builder
	b.WriteString(string(p.Kind))
	b.WriteByte('|')
	b.WriteString(strings.ToLower(strings.TrimSpace(p.Title)))
	b.WriteByte('|')
	for _, o := range p.Options {
		b.WriteString(strings.ToLower(strings.TrimSpace(o.Label)))
		if o.Checked {
			b.WriteString(":1")
		} else {
			b.WriteString(":0")
		}
		if o.NeedsText {
			b.WriteString(":T")
		}
		b.WriteByte(';')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// ExtractPendingQuestionFromItems scans only the active trailing turn of parsed conversation items
// and returns an unanswered AskUserQuestion / ask_question / ask_user_question tool call, if any.
// Stops immediately at newer user/assistant messages or completed tools so cancelled historical
// questions never stay latched for the rest of the session.
func ExtractPendingQuestionFromItems(items []ChatTurnItem) (PendingQuestion, bool) {
	for i := len(items) - 1; i >= 0; i-- {
		it := items[i]
		if it.Role == RoleUser || it.Role == RoleAssistant {
			return PendingQuestion{}, false
		}
		if it.Role != RoleToolGroup {
			continue
		}
		for j := len(it.Tools) - 1; j >= 0; j-- {
			tc := it.Tools[j]
			if tc.HasResult {
				return PendingQuestion{}, false
			}
			if !isQuestionToolName(tc.Name) {
				continue
			}
			pq := parsePendingQuestionJSON(tc.ID, tc.Name, tc.RawInput)
			if len(pq.Questions) > 0 {
				return pq, true
			}
			return PendingQuestion{
				ToolID:   tc.ID,
				ToolName: tc.Name,
			}, true
		}
	}
	return PendingQuestion{}, false
}

func parsePendingQuestionJSON(toolID, toolName, raw string) PendingQuestion {
	out := PendingQuestion{ToolID: toolID, ToolName: toolName}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	var payload struct {
		Questions []struct {
			Question      string `json:"question"`
			Header        string `json:"header"`
			MultiSelect   bool   `json:"multiSelect"`
			IsMultiSelect bool   `json:"is_multi_select"`
			Options       []json.RawMessage `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return out
	}
	for _, q := range payload.Questions {
		item := PendingQuestionItem{
			Question:    strings.TrimSpace(q.Question),
			Header:      strings.TrimSpace(q.Header),
			MultiSelect: q.MultiSelect || q.IsMultiSelect,
		}
		for _, rawOpt := range q.Options {
			var strOpt string
			if json.Unmarshal(rawOpt, &strOpt) == nil {
				if s := strings.TrimSpace(strOpt); s != "" {
					item.Options = append(item.Options, PendingQuestionOption{Label: s})
				}
				continue
			}
			var objOpt struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			}
			if json.Unmarshal(rawOpt, &objOpt) == nil && strings.TrimSpace(objOpt.Label) != "" {
				item.Options = append(item.Options, PendingQuestionOption{
					Label:       strings.TrimSpace(objOpt.Label),
					Description: strings.TrimSpace(objOpt.Description),
				})
			}
		}
		if item.Question != "" || len(item.Options) > 0 {
			out.Questions = append(out.Questions, item)
		}
	}
	return out
}

// EnrichPromptFromJSONL supplements a live-parsed TUI question prompt with full question text,
// option descriptions, and multiSelect hints from the corresponding unanswered JSONL tool call,
// while preserving the live TUI's exact option count, order, cursor, and fingerprint.
func EnrichPromptFromJSONL(p InteractivePrompt, pq PendingQuestion) InteractivePrompt {
	if len(pq.Questions) == 0 || p.Kind == PromptKindReview {
		return p
	}
	out := p
	out.Options = append([]PromptOption(nil), p.Options...)

	// Match active question item by title similarity or ActiveTab index.
	var matched *PendingQuestionItem
	lowTitle := strings.ToLower(strings.TrimSpace(p.Title))
	for i := range pq.Questions {
		qLow := strings.ToLower(strings.TrimSpace(pq.Questions[i].Question))
		if qLow != "" && lowTitle != "" && (strings.Contains(qLow, lowTitle) || strings.Contains(lowTitle, qLow)) {
			matched = &pq.Questions[i]
			break
		}
	}
	if matched == nil && p.ActiveTab >= 0 && p.ActiveTab < len(pq.Questions) {
		matched = &pq.Questions[p.ActiveTab]
	}
	if matched == nil && len(pq.Questions) == 1 {
		matched = &pq.Questions[0]
	}
	if matched == nil {
		return out
	}

	if matched.Question != "" && len(matched.Question) > len(out.Title) {
		out.Title = matched.Question
	}
	if matched.MultiSelect {
		out.MultiSelect = true
	}
	for i := range out.Options {
		if out.Options[i].NeedsText || out.Options[i].IsSubmit {
			continue
		}
		optLow := strings.ToLower(strings.TrimSpace(out.Options[i].Label))
		for _, jqOpt := range matched.Options {
			jqLow := strings.ToLower(strings.TrimSpace(jqOpt.Label))
			if jqLow != "" && (optLow == jqLow || strings.HasPrefix(optLow, jqLow) || strings.HasPrefix(jqLow, optLow)) {
				if jqOpt.Description != "" && out.Options[i].Description == "" {
					out.Options[i].Description = jqOpt.Description
				}
				break
			}
		}
	}
	return out
}

// PromptTransport is the minimal RPC subset needed by DriveInteractivePrompt (satisfied by *Client).
type PromptTransport interface {
	Get(ctx context.Context, addr string) (Terminal, error)
	Read(ctx context.Context, addr string, lines int) (ReadResult, error)
	Keys(ctx context.Context, addr string, keys ...string) (KeysResult, error)
	SendForced(ctx context.Context, addr, text string) (SendResult, error)
}

// PromptSubmitRequest describes a user action against a live InteractivePrompt.
type PromptSubmitRequest struct {
	Expected        InteractivePrompt
	OptionIndex     int    // target option for single-select / permission / review
	SelectedIndices []int  // desired checked indices for multi-select submission
	CustomText      string // optional free-text for NeedsText ("Other" / Claude option 3)
	IsMultiSubmit   bool   // true when submitting a multi-select question
	TabDelta        int    // non-zero to switch multi-question tabs (-1 = prev, +1 = next)
	RequireBlocked  bool   // enforce live terminal.StateWord() == "blocked"
}

// DriveInteractivePrompt safely re-verifies the remote terminal state and live screen
// fingerprint before sending exact navigation, toggle, text, or confirmation inputs.
func DriveInteractivePrompt(ctx context.Context, tr PromptTransport, addr string, req PromptSubmitRequest) error {
	if tr == nil {
		return errors.New("实例未连接")
	}
	latest, err := tr.Get(ctx, addr)
	if err != nil {
		return err
	}
	if latest.Finished() {
		return errors.New("会话已结束，已取消操作")
	}
	if req.RequireBlocked && latest.StateWord() != "blocked" {
		return errors.New("状态已变化，已取消操作")
	}

	rr, err := tr.Read(ctx, addr, 0)
	if err != nil {
		return err
	}
	statusKind := ""
	if latest.Status != nil {
		statusKind = latest.Status.Kind
	}
	if statusKind == "" && (req.Expected.Kind == PromptKindQuestion || req.Expected.Kind == PromptKindReview) {
		statusKind = "question"
	}
	harness := req.Expected.Harness
	if harness == "" {
		harness = DetectHarnessKind(latest)
	}
	live, ok := ParseInteractivePrompt(rr.Text, harness, statusKind)
	if !ok {
		return errors.New("终端界面已变化，请进终端确认")
	}
	if req.Expected.Fingerprint != "" && live.Fingerprint != req.Expected.Fingerprint {
		return errors.New("选项已变化，已取消操作")
	}

	// 1. Multi-question tab navigation.
	if req.TabDelta != 0 {
		key := "tab"
		n := req.TabDelta
		if n < 0 {
			key = "shift+tab"
			n = -n
		}
		keys := make([]string, n)
		for i := range keys {
			keys[i] = key
		}
		_, err = tr.Keys(ctx, addr, keys...)
		return err
	}

	// 2. Multi-select toggle diff + optional custom text + submit.
	if req.IsMultiSubmit {
		wantSet := make(map[int]bool, len(req.SelectedIndices))
		for _, idx := range req.SelectedIndices {
			wantSet[idx] = true
		}
		cur := live.Cursor
		var batch []string
		moveTo := func(target int) {
			for cur < target {
				batch = append(batch, "down")
				cur++
			}
			for cur > target {
				batch = append(batch, "up")
				cur--
			}
		}
		submitIdx := -1
		otherIdx := -1
		for _, o := range live.Options {
			if o.IsSubmit {
				submitIdx = o.Index
				continue
			}
			if o.NeedsText {
				otherIdx = o.Index
				continue
			}
			if wantSet[o.Index] != o.Checked {
				moveTo(o.Index)
				batch = append(batch, "space")
			}
		}
		custom := strings.TrimSpace(req.CustomText)
		if custom != "" && otherIdx >= 0 {
			moveTo(otherIdx)
			if !live.Options[otherIdx].InlineTextMode {
				batch = append(batch, "enter")
			}
			if len(batch) > 0 {
				if _, err := tr.Keys(ctx, addr, batch...); err != nil {
					return err
				}
			}
			if _, err := tr.SendForced(ctx, addr, custom); err != nil {
				return err
			}
			// If the harness did not auto-submit on custom text Enter (e.g. requires explicit Next/Submit row),
			// verify via a quick screen read and trigger submitIdx only if still on the same question.
			if submitIdx >= 0 {
				if after, rerr := tr.Read(ctx, addr, 0); rerr == nil {
					if still, ok := ParseInteractivePrompt(after.Text, harness, statusKind); ok && still.Fingerprint == live.Fingerprint {
						var finish []string
						for c := still.Cursor; c < submitIdx; c++ {
							finish = append(finish, "down")
						}
						for c := still.Cursor; c > submitIdx; c-- {
							finish = append(finish, "up")
						}
						finish = append(finish, "enter")
						_, err := tr.Keys(ctx, addr, finish...)
						return err
					}
				}
			}
			return nil
		}
		if submitIdx >= 0 {
			moveTo(submitIdx)
		}
		batch = append(batch, "enter")
		_, err := tr.Keys(ctx, addr, batch...)
		return err
	}

	// 3. Single-select / permission / review choice.
	if req.OptionIndex < 0 || req.OptionIndex >= len(live.Options) {
		return errors.New("所选项已失效，请刷新后重试")
	}
	targetOpt := live.Options[req.OptionIndex]
	if req.OptionIndex < len(req.Expected.Options) {
		wantLbl := strings.ToLower(strings.TrimSpace(req.Expected.Options[req.OptionIndex].Label))
		gotLbl := strings.ToLower(strings.TrimSpace(targetOpt.Label))
		if wantLbl != "" && gotLbl != wantLbl {
			return errors.New("选项文案已变化，已取消操作")
		}
	}

	var moveKeys []string
	for cur := live.Cursor; cur < req.OptionIndex; cur++ {
		moveKeys = append(moveKeys, "down")
	}
	for cur := live.Cursor; cur > req.OptionIndex; cur-- {
		moveKeys = append(moveKeys, "up")
	}

	if targetOpt.NeedsText {
		custom := strings.TrimSpace(req.CustomText)
		if custom == "" {
			if targetOpt.Tone == ToneDanger {
				_, err := tr.Keys(ctx, addr, "esc")
				return err
			}
			return errors.New("请先输入自定义回复内容")
		}
		if !targetOpt.InlineTextMode {
			moveKeys = append(moveKeys, "enter")
		}
		if len(moveKeys) > 0 {
			if _, err := tr.Keys(ctx, addr, moveKeys...); err != nil {
				return err
			}
		}
		_, err := tr.SendForced(ctx, addr, custom)
		return err
	}

	moveKeys = append(moveKeys, "enter")
	_, err = tr.Keys(ctx, addr, moveKeys...)
	return err
}
