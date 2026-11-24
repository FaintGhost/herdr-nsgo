package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ChatRole identifies one card or collapsible row in Chat View.
type ChatRole string

const (
	RoleUser      ChatRole = "user"
	RoleAssistant ChatRole = "assistant"
	RoleThinking  ChatRole = "thinking"
	RoleToolGroup ChatRole = "tool_group"
)

// TranscriptFormat identifies the underlying transcript schema.
type TranscriptFormat string

const (
	FormatClaude      TranscriptFormat = "claude"
	FormatPiOmp       TranscriptFormat = "pi-omp"
	FormatCodex       TranscriptFormat = "codex"
	FormatAntigravity TranscriptFormat = "antigravity"
	FormatVTFallback  TranscriptFormat = "vt-fallback"
)

// MarkdownBlockKind classifies segments inside an assistant reply.
type MarkdownBlockKind uint8

const (
	BlockParagraph MarkdownBlockKind = iota
	BlockList
	BlockCode
)

// MarkdownBlock is one structured visual block inside RoleAssistant.
type MarkdownBlock struct {
	Kind  MarkdownBlockKind
	Lang  string
	Text  string
	Items []string
}

// ChatToolCall represents one tool invocation and its paired result summary.
type ChatToolCall struct {
	ID           string
	Name         string
	Target       string
	InputSummary string
	Output       string
	IsError      bool
}

// ChatTurnItem is the immutable normalized view-model item rendered by screen_chat.go.
type ChatTurnItem struct {
	ID         string
	Role       ChatRole
	Text       string
	FromAgent  string
	Model      string
	App        string
	Blocks     []MarkdownBlock
	Tools      []ChatToolCall
	Optimistic bool
}

const maxSafeJSONLLineBytes = 512 * 1024

var (
	reSystemReminder = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	reAddMetadata    = regexp.MustCompile(`(?s)<ADDITIONAL_METADATA>.*?</ADDITIONAL_METADATA>`)
	reUserRequestTag = regexp.MustCompile(`(?s)^\s*<USER_REQUEST>\s*(.*?)\s*</USER_REQUEST>\s*$`)
	reHCReport       = regexp.MustCompile(`(?s)^\s*<hc\b([^>]*)>(.*?)</hc>\s*$`)
	reHCFromAttr     = regexp.MustCompile(`from="([^"]+)"`)
)

// IsAgentTerminal reports whether t runs an AI coding harness (defaulting to Chat View)
// rather than a plain interactive shell (defaulting to Terminal View).
func IsAgentTerminal(t Terminal) bool {
	if kind := DetectHarnessKind(t); kind != "" {
		return true
	}
	if t.Template != nil && strings.TrimSpace(*t.Template) != "" {
		return true
	}
	return false
}

// DetectHarnessKind returns a normalized harness identifier ("claude", "omp", "pi",
// "antigravity", "codex", "other-agent", or "") from terminal metadata.
func DetectHarnessKind(t Terminal) string {
	candidates := []string{t.AppName()}
	if t.Labels != nil {
		if alma := t.Labels["alma.session"]; alma != "" {
			candidates = append(candidates, alma)
		}
	}
	if t.Foreground != nil {
		candidates = append(candidates, t.Foreground.Name)
		candidates = append(candidates, t.Foreground.Argv...)
	}
	candidates = append(candidates, t.Argv...)

	for _, raw := range candidates {
		s := strings.ToLower(strings.TrimSpace(raw))
		if s == "" {
			continue
		}
		base := filepath.Base(s)
		switch {
		case strings.Contains(s, "claude"):
			return "claude"
		case strings.HasPrefix(s, "omp:") || base == "omp" || strings.HasPrefix(s, "omp ") || strings.Contains(s, " omp ") || strings.Contains(s, "oh-my-pi"):
			return "omp"
		case strings.HasPrefix(s, "pi:") || base == "pi" || strings.HasPrefix(s, "pi ") || strings.Contains(s, " pi "):
			return "pi"
		case strings.Contains(s, "antigravity") || base == "agy" || strings.HasPrefix(s, "agy ") || strings.Contains(s, "gemini"):
			return "antigravity"
		case strings.Contains(s, "codex"):
			return "codex"
		case strings.Contains(s, "opencode") || strings.Contains(s, "aider") || strings.Contains(s, "cursor"):
			return "other-agent"
		}
	}
	return ""
}

// SplitMarkdownBlocks splits markdown text into paragraphs, bullet/numbered lists, and fenced code blocks.
func SplitMarkdownBlocks(text string) []MarkdownBlock {
	norm := strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n")
	if norm == "" {
		return nil
	}
	lines := strings.Split(norm, "\n")
	var out []MarkdownBlock

	var para []string
	var listItems []string
	inCode := false
	codeLang := ""
	var codeLines []string

	flushPara := func() {
		if len(para) > 0 {
			joined := strings.TrimSpace(strings.Join(para, "\n"))
			if joined != "" {
				out = append(out, MarkdownBlock{Kind: BlockParagraph, Text: joined})
			}
			para = nil
		}
	}
	flushList := func() {
		if len(listItems) > 0 {
			cp := make([]string, len(listItems))
			copy(cp, listItems)
			out = append(out, MarkdownBlock{Kind: BlockList, Items: cp, Text: strings.Join(cp, "\n")})
			listItems = nil
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				out = append(out, MarkdownBlock{
					Kind: BlockCode,
					Lang: codeLang,
					Text: strings.TrimRight(strings.Join(codeLines, "\n"), "\n"),
				})
				inCode = false
				codeLang = ""
				codeLines = nil
			} else {
				flushPara()
				flushList()
				inCode = true
				codeLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			}
			continue
		}
		if inCode {
			codeLines = append(codeLines, line)
			continue
		}
		if trimmed == "" {
			flushPara()
			flushList()
			continue
		}
		if item, ok := parseListItem(trimmed); ok {
			flushPara()
			listItems = append(listItems, item)
			continue
		}
		flushList()
		para = append(para, line)
	}
	if inCode {
		out = append(out, MarkdownBlock{
			Kind: BlockCode,
			Lang: codeLang,
			Text: strings.TrimRight(strings.Join(codeLines, "\n"), "\n"),
		})
	} else {
		flushPara()
		flushList()
	}
	return out
}

func parseListItem(trimmed string) (string, bool) {
	for _, p := range []string{"- ", "* ", "• "} {
		if strings.HasPrefix(trimmed, p) {
			return strings.TrimSpace(trimmed[len(p):]), true
		}
	}
	// Numbered list: "1. foo"
	dot := strings.IndexByte(trimmed, '.')
	if dot > 0 && dot <= 3 && len(trimmed) > dot+1 && trimmed[dot+1] == ' ' {
		allDigits := true
		for i := 0; i < dot; i++ {
			if trimmed[i] < '0' || trimmed[i] > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return trimmed, true
		}
	}
	return "", false
}

func cleanUserPrompt(raw string) (text, fromAgent string) {
	s := reSystemReminder.ReplaceAllString(raw, "")
	s = reAddMetadata.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	if m := reUserRequestTag.FindStringSubmatch(s); len(m) == 2 {
		s = strings.TrimSpace(m[1])
	}
	if m := reHCReport.FindStringSubmatch(s); len(m) == 3 {
		if fm := reHCFromAttr.FindStringSubmatch(m[1]); len(fm) == 2 {
			fromAgent = strings.TrimSpace(fm[1])
		} else {
			fromAgent = "hc"
		}
		s = strings.TrimSpace(m[2])
	}
	return s, fromAgent
}

func truncateToolOutput(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if s == "" {
		return ""
	}
	if len(s) > 16384 {
		s = s[:8192] + "\n… [输出超长已裁剪] …\n" + s[len(s)-8192:]
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= 40 {
		return s
	}
	omitted := len(lines) - 40
	head := strings.Join(lines[:20], "\n")
	tail := strings.Join(lines[len(lines)-20:], "\n")
	return fmt.Sprintf("%s\n…（省略中间 %d 行）…\n%s", head, omitted, tail)
}

func extractToolTarget(name string, rawArgs any) (target, summary string) {
	var m map[string]any
	switch v := rawArgs.(type) {
	case map[string]any:
		m = v
	case string:
		if strings.TrimSpace(v) != "" {
			if json.Unmarshal([]byte(v), &m) != nil {
				s := singleLineClip(v, 80)
				return s, truncateToolOutput(v)
			}
		}
	case json.RawMessage:
		if len(v) > 0 {
			if json.Unmarshal(v, &m) != nil {
				s := singleLineClip(string(v), 80)
				return s, truncateToolOutput(string(v))
			}
		}
	}
	if len(m) == 0 {
		return "", ""
	}
	for _, k := range []string{"file_path", "path", "Path", "command", "CommandLine", "cmd", "pattern", "query", "url", "Terminal", "terminal"} {
		if val, ok := m[k]; ok {
			switch tv := val.(type) {
			case string:
				if tv != "" {
					if strings.Contains(strings.ToLower(k), "path") {
						target = filepath.Base(tv)
						if target == "." || target == "/" {
							target = tv
						}
					} else {
						target = singleLineClip(tv, 64)
					}
				}
			case []any:
				var parts []string
				for _, elem := range tv {
					parts = append(parts, fmt.Sprint(elem))
				}
				target = singleLineClip(strings.Join(parts, " "), 64)
			}
			if target != "" {
				break
			}
		}
	}
	rawBytes, _ := json.Marshal(m)
	summary = truncateToolOutput(string(rawBytes))
	_ = name
	return target, summary
}

func singleLineClip(s string, maxRunes int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) > maxRunes {
		return string(r[:maxRunes]) + "…"
	}
	return s
}

// ParseTranscriptLines dispatches lines to the matching Harness JSONL parser.
func ParseTranscriptLines(format TranscriptFormat, lines []string) []ChatTurnItem {
	switch format {
	case FormatClaude:
		return ParseClaudeJSONL(lines)
	case FormatPiOmp:
		return ParsePiOmpJSONL(lines)
	case FormatCodex:
		return ParseCodexJSONL(lines)
	case FormatAntigravity:
		return ParseAntigravityJSONL(lines)
	default:
		return ParseVTTranscript(strings.Join(lines, "\n"))
	}
}

// appendOrMergeTool appends a ChatToolCall, coalescing adjacent tool calls into one RoleToolGroup.
func appendToolItem(items *[]ChatTurnItem, id string, tc ChatToolCall, toolIdx map[string][2]int) {
	n := len(*items)
	if n > 0 && (*items)[n-1].Role == RoleToolGroup {
		grpIdx := n - 1
		subIdx := len((*items)[grpIdx].Tools)
		(*items)[grpIdx].Tools = append((*items)[grpIdx].Tools, tc)
		if tc.ID != "" {
			toolIdx[tc.ID] = [2]int{grpIdx, subIdx}
		}
		return
	}
	grpIdx := n
	*items = append(*items, ChatTurnItem{
		ID:    id,
		Role:  RoleToolGroup,
		Tools: []ChatToolCall{tc},
	})
	if tc.ID != "" {
		toolIdx[tc.ID] = [2]int{grpIdx, 0}
	}
}

func attachToolResult(items []ChatTurnItem, toolIdx map[string][2]int, callID, output string, isErr bool) bool {
	loc, ok := toolIdx[callID]
	if !ok || loc[0] >= len(items) || loc[1] >= len(items[loc[0]].Tools) {
		return false
	}
	items[loc[0]].Tools[loc[1]].Output = truncateToolOutput(output)
	if isErr {
		items[loc[0]].Tools[loc[1]].IsError = true
	}
	return true
}

// ---------------------------------------------------------------------------
// 1. Claude Code (~/.claude/projects/*/*.jsonl)

func ParseClaudeJSONL(lines []string) []ChatTurnItem {
	var items []ChatTurnItem
	toolIdx := make(map[string][2]int)
	seenUUID := make(map[string]bool)
	lastAsstMsgID := ""

	for lineNo, raw := range lines {
		raw = strings.TrimSpace(raw)
		if raw == "" || len(raw) > maxSafeJSONLLineBytes {
			continue
		}
		var env struct {
			Type    string `json:"type"`
			UUID    string `json:"uuid"`
			Message struct {
				ID      string          `json:"id"`
				Model   string          `json:"model"`
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(raw), &env) != nil {
			continue
		}
		if env.UUID != "" {
			if seenUUID[env.UUID] {
				continue
			}
			seenUUID[env.UUID] = true
		}

		switch env.Type {
		case "user":
			// Content can be a string OR array of blocks (including tool_result).
			var strContent string
			if json.Unmarshal(env.Message.Content, &strContent) == nil {
				txt, from := cleanUserPrompt(strContent)
				if txt != "" {
					items = append(items, ChatTurnItem{
						ID:        nonEmpty(env.UUID, fmt.Sprintf("cu-%d", lineNo)),
						Role:      RoleUser,
						Text:      txt,
						FromAgent: from,
					})
				}
				continue
			}
			var blocks []struct {
				Type      string          `json:"type"`
				Text      string          `json:"text"`
				ToolUseID string          `json:"tool_use_id"`
				Content   json.RawMessage `json:"content"`
				IsError   bool            `json:"is_error"`
			}
			if json.Unmarshal(env.Message.Content, &blocks) != nil {
				continue
			}
			var userParts []string
			for _, b := range blocks {
				switch b.Type {
				case "tool_result":
					out := extractClaudeToolResultContent(b.Content)
					attachToolResult(items, toolIdx, b.ToolUseID, out, b.IsError)
				case "text":
					if t, _ := cleanUserPrompt(b.Text); t != "" {
						userParts = append(userParts, t)
					}
				case "image":
					userParts = append(userParts, "[图片附件]")
				}
			}
			if len(userParts) > 0 {
				joined, from := cleanUserPrompt(strings.Join(userParts, "\n"))
				if joined != "" {
					items = append(items, ChatTurnItem{
						ID:        nonEmpty(env.UUID, fmt.Sprintf("cu-%d", lineNo)),
						Role:      RoleUser,
						Text:      joined,
						FromAgent: from,
					})
				}
			}

		case "assistant":
			var blocks []struct {
				Type     string          `json:"type"`
				Text     string          `json:"text"`
				Thinking string          `json:"thinking"`
				ID       string          `json:"id"`
				Name     string          `json:"name"`
				Input    json.RawMessage `json:"input"`
			}
			if json.Unmarshal(env.Message.Content, &blocks) != nil {
				continue
			}
			msgID := nonEmpty(env.Message.ID, nonEmpty(env.UUID, fmt.Sprintf("ca-%d", lineNo)))
			for bi, b := range blocks {
				switch b.Type {
				case "thinking":
					th := strings.TrimSpace(b.Thinking)
					if th == "" {
						continue
					}
					if len(items) > 0 && items[len(items)-1].Role == RoleThinking && lastAsstMsgID == msgID {
						items[len(items)-1].Text += "\n" + th
					} else {
						items = append(items, ChatTurnItem{
							ID:    fmt.Sprintf("%s-th-%d-%d", msgID, lineNo, bi),
							Role:  RoleThinking,
							Text:  th,
							Model: env.Message.Model,
							App:   "claude-code",
						})
					}
				case "tool_use":
					target, summary := extractToolTarget(b.Name, b.Input)
					appendToolItem(&items, fmt.Sprintf("%s-tg-%d", msgID, lineNo), ChatToolCall{
						ID:           b.ID,
						Name:         b.Name,
						Target:       target,
						InputSummary: summary,
					}, toolIdx)
				case "text":
					txt := strings.TrimSpace(b.Text)
					if txt == "" {
						continue
					}
					if len(items) > 0 && items[len(items)-1].Role == RoleAssistant && lastAsstMsgID == msgID {
						merged := items[len(items)-1].Text + "\n\n" + txt
						items[len(items)-1].Text = merged
						items[len(items)-1].Blocks = SplitMarkdownBlocks(merged)
					} else {
						items = append(items, ChatTurnItem{
							ID:     fmt.Sprintf("%s-txt-%d-%d", msgID, lineNo, bi),
							Role:   RoleAssistant,
							Text:   txt,
							Model:  env.Message.Model,
							App:    "claude-code",
							Blocks: SplitMarkdownBlocks(txt),
						})
					}
				}
			}
			lastAsstMsgID = msgID
		}
	}
	return items
}

func extractClaudeToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var arr []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &arr) == nil {
		var parts []string
		for _, el := range arr {
			if el.Type == "image" {
				parts = append(parts, "[图片附件]")
			} else if el.Text != "" {
				parts = append(parts, el.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

// ---------------------------------------------------------------------------
// 2. OMP & Pi (~/.omp/agent/sessions/*/*.jsonl & ~/.pi/agent/sessions/*/*.jsonl)

func ParsePiOmpJSONL(lines []string) []ChatTurnItem {
	var items []ChatTurnItem
	toolIdx := make(map[string][2]int)

	for lineNo, raw := range lines {
		raw = strings.TrimSpace(raw)
		if raw == "" || len(raw) > maxSafeJSONLLineBytes {
			continue
		}
		var env struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Message struct {
				Role       string          `json:"role"`
				Model      string          `json:"model"`
				ToolCallID string          `json:"toolCallId"`
				ToolName   string          `json:"toolName"`
				IsError    bool            `json:"isError"`
				Content    json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(raw), &env) != nil || env.Type != "message" {
			continue
		}
		rowID := nonEmpty(env.ID, fmt.Sprintf("omp-%d", lineNo))

		switch env.Message.Role {
		case "user":
			txt := extractPiContentText(env.Message.Content)
			cleaned, from := cleanUserPrompt(txt)
			if cleaned != "" {
				items = append(items, ChatTurnItem{
					ID:        rowID,
					Role:      RoleUser,
					Text:      cleaned,
					FromAgent: from,
				})
			}
		case "assistant":
			var blocks []struct {
				Type      string          `json:"type"`
				Text      string          `json:"text"`
				Thinking  string          `json:"thinking"`
				ID        string          `json:"id"`
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if json.Unmarshal(env.Message.Content, &blocks) != nil {
				if s := extractPiContentText(env.Message.Content); s != "" {
					items = append(items, ChatTurnItem{
						ID:     rowID,
						Role:   RoleAssistant,
						Text:   s,
						Model:  env.Message.Model,
						App:    "omp",
						Blocks: SplitMarkdownBlocks(s),
					})
				}
				continue
			}
			for bi, b := range blocks {
				switch b.Type {
				case "thinking":
					if th := strings.TrimSpace(b.Thinking); th != "" {
						items = append(items, ChatTurnItem{
							ID:    fmt.Sprintf("%s-th-%d", rowID, bi),
							Role:  RoleThinking,
							Text:  th,
							Model: env.Message.Model,
							App:   "omp",
						})
					}
				case "toolCall":
					target, summary := extractToolTarget(b.Name, b.Arguments)
					appendToolItem(&items, fmt.Sprintf("%s-tg-%d", rowID, bi), ChatToolCall{
						ID:           b.ID,
						Name:         b.Name,
						Target:       target,
						InputSummary: summary,
					}, toolIdx)
				case "text":
					if txt := strings.TrimSpace(b.Text); txt != "" {
						items = append(items, ChatTurnItem{
							ID:     fmt.Sprintf("%s-txt-%d", rowID, bi),
							Role:   RoleAssistant,
							Text:   txt,
							Model:  env.Message.Model,
							App:    "omp",
							Blocks: SplitMarkdownBlocks(txt),
						})
					}
				}
			}
		case "toolResult":
			out := extractPiContentText(env.Message.Content)
			if !attachToolResult(items, toolIdx, env.Message.ToolCallID, out, env.Message.IsError) && out != "" {
				appendToolItem(&items, rowID, ChatToolCall{
					ID:      env.Message.ToolCallID,
					Name:    nonEmpty(env.Message.ToolName, "tool"),
					Output:  truncateToolOutput(out),
					IsError: env.Message.IsError,
				}, toolIdx)
			}
		}
	}
	return items
}

func extractPiContentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var arr []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &arr) == nil {
		var parts []string
		for _, b := range arr {
			if b.Type == "image" {
				parts = append(parts, "[图片附件]")
			} else if strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// ---------------------------------------------------------------------------
// 3. Codex (~/.codex/sessions/*/*/*/*.jsonl — authoritative response_item stream)

func ParseCodexJSONL(lines []string) []ChatTurnItem {
	var items []ChatTurnItem
	toolIdx := make(map[string][2]int)
	curModel := ""

	for lineNo, raw := range lines {
		raw = strings.TrimSpace(raw)
		if raw == "" || len(raw) > maxSafeJSONLLineBytes {
			continue
		}
		var env struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(raw), &env) != nil {
			continue
		}
		if env.Type == "turn_context" {
			var tc struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(env.Payload, &tc) == nil && tc.Model != "" {
				curModel = tc.Model
			}
			continue
		}
		if env.Type != "response_item" {
			// Explicitly ignore event_msg mirrors to prevent duplicate rendering.
			continue
		}

		var item struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Role      string          `json:"role"`
			CallID    string          `json:"call_id"`
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Input     json.RawMessage `json:"input"`
			Output    json.RawMessage `json:"output"`
			Content   json.RawMessage `json:"content"`
			Summary   []struct {
				Text string `json:"text"`
			} `json:"summary"`
		}
		if json.Unmarshal(env.Payload, &item) != nil {
			continue
		}
		rowID := nonEmpty(item.ID, fmt.Sprintf("cx-%d", lineNo))

		switch item.Type {
		case "message", "agent_message":
			txt := extractCodexContentText(item.Content)
			if txt == "" {
				continue
			}
			role := item.Role
			if item.Type == "agent_message" && role == "" {
				role = "assistant"
			}
			if role == "user" {
				cleaned, from := cleanUserPrompt(txt)
				if cleaned != "" {
					items = append(items, ChatTurnItem{
						ID:        rowID,
						Role:      RoleUser,
						Text:      cleaned,
						FromAgent: from,
					})
				}
			} else if role == "assistant" {
				items = append(items, ChatTurnItem{
					ID:     rowID,
					Role:   RoleAssistant,
					Text:   txt,
					Model:  curModel,
					App:    "codex",
					Blocks: SplitMarkdownBlocks(txt),
				})
			}
		case "reasoning":
			var parts []string
			for _, s := range item.Summary {
				if strings.TrimSpace(s.Text) != "" {
					parts = append(parts, strings.TrimSpace(s.Text))
				}
			}
			if len(parts) > 0 {
				items = append(items, ChatTurnItem{
					ID:    rowID,
					Role:  RoleThinking,
					Text:  strings.Join(parts, "\n"),
					Model: curModel,
					App:   "codex",
				})
			}
		case "function_call", "custom_tool_call":
			rawArgs := item.Arguments
			if len(rawArgs) == 0 {
				rawArgs = item.Input
			}
			target, summary := extractToolTarget(item.Name, rawArgs)
			appendToolItem(&items, rowID, ChatToolCall{
				ID:           item.CallID,
				Name:         item.Name,
				Target:       target,
				InputSummary: summary,
			}, toolIdx)
		case "function_call_output", "custom_tool_call_output":
			outStr := extractCodexContentText(item.Output)
			isErr := strings.Contains(outStr, "Exit code: 1") || strings.HasPrefix(strings.TrimSpace(outStr), "error:")
			attachToolResult(items, toolIdx, item.CallID, outStr, isErr)
		}
	}
	return items
}

func extractCodexContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var arr []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &arr) == nil {
		var parts []string
		for _, el := range arr {
			if strings.Contains(el.Type, "image") {
				parts = append(parts, "[图片附件]")
			} else if strings.TrimSpace(el.Text) != "" {
				parts = append(parts, strings.TrimSpace(el.Text))
			}
		}
		return strings.Join(parts, "\n")
	}
	return strings.TrimSpace(string(raw))
}

// ---------------------------------------------------------------------------
// 4. Antigravity (~/.gemini/antigravity-cli/brain/*/.system_generated/logs/transcript.jsonl)

func ParseAntigravityJSONL(lines []string) []ChatTurnItem {
	var items []ChatTurnItem
	var pendingToolRefs [][2]int

	for lineNo, raw := range lines {
		raw = strings.TrimSpace(raw)
		if raw == "" || len(raw) > maxSafeJSONLLineBytes {
			continue
		}
		var row struct {
			StepIndex int    `json:"step_index"`
			Source    string `json:"source"`
			Type      string `json:"type"`
			Status    string `json:"status"`
			Model     string `json:"model"`
			Thinking  string `json:"thinking"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"tool_calls"`
		}
		if json.Unmarshal([]byte(raw), &row) != nil {
			continue
		}
		rowID := fmt.Sprintf("agy-%d-%d", row.StepIndex, lineNo)

		switch row.Type {
		case "USER_INPUT":
			pendingToolRefs = nil
			txt, from := cleanUserPrompt(row.Content)
			if txt != "" {
				items = append(items, ChatTurnItem{
					ID:        rowID,
					Role:      RoleUser,
					Text:      txt,
					FromAgent: from,
				})
			}
		case "PLANNER_RESPONSE":
			pendingToolRefs = nil
			if th := strings.TrimSpace(row.Thinking); th != "" {
				items = append(items, ChatTurnItem{
					ID:    rowID + "-th",
					Role:  RoleThinking,
					Text:  th,
					Model: row.Model,
					App:   "antigravity",
				})
			}
			if txt := strings.TrimSpace(row.Content); txt != "" {
				items = append(items, ChatTurnItem{
					ID:     rowID + "-txt",
					Role:   RoleAssistant,
					Text:   txt,
					Model:  row.Model,
					App:    "antigravity",
					Blocks: SplitMarkdownBlocks(txt),
				})
			}
			if len(row.ToolCalls) > 0 {
				dummyMap := make(map[string][2]int)
				for ti, tc := range row.ToolCalls {
					callKey := fmt.Sprintf("%s-tc-%d", rowID, ti)
					target, summary := extractToolTarget(tc.Name, tc.Args)
					appendToolItem(&items, rowID+"-tg", ChatToolCall{
						ID:           callKey,
						Name:         tc.Name,
						Target:       target,
						InputSummary: summary,
					}, dummyMap)
					pendingToolRefs = append(pendingToolRefs, dummyMap[callKey])
				}
			}
		case "GENERIC":
			if row.Source == "MODEL" && len(pendingToolRefs) > 0 {
				loc := pendingToolRefs[0]
				pendingToolRefs = pendingToolRefs[1:]
				if loc[0] < len(items) && loc[1] < len(items[loc[0]].Tools) {
					items[loc[0]].Tools[loc[1]].Output = truncateToolOutput(row.Content)
					if (row.Status != "" && row.Status != "DONE") || strings.Contains(row.Content, "Command exited with code 1") {
						items[loc[0]].Tools[loc[1]].IsError = true
					}
				}
			}
		}
	}
	return items
}

// ---------------------------------------------------------------------------
// 5. Universal VT / terminal.read Transcript Fallback

func ParseVTTranscript(raw string) []ChatTurnItem {
	norm := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(norm, "\n")

	var items []ChatTurnItem
	var curRole ChatRole
	var curLines []string

	flush := func(idx int) {
		if curRole == "" || len(curLines) == 0 {
			curLines = nil
			return
		}
		txt := strings.TrimSpace(strings.Join(curLines, "\n"))
		curLines = nil
		if txt == "" {
			return
		}
		item := ChatTurnItem{
			ID:   fmt.Sprintf("vt-%d", idx),
			Role: curRole,
			Text: txt,
		}
		if curRole == RoleAssistant {
			item.Blocks = SplitMarkdownBlocks(txt)
		}
		items = append(items, item)
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(curLines) > 0 {
				curLines = append(curLines, "")
			}
			continue
		}
		if isTUIStatusChrome(trimmed) {
			continue
		}
		if promptText, ok := cutVTPrompt(trimmed); ok {
			flush(i)
			curRole = RoleUser
			if promptText != "" {
				curLines = append(curLines, promptText)
			}
			continue
		}
		if bulletText, isToolSub := cutVTAgentMarker(trimmed); bulletText != "" || isToolSub {
			if !isToolSub {
				flush(i)
				curRole = RoleAssistant
			} else if curRole == "" {
				curRole = RoleAssistant
			}
			if bulletText != "" {
				curLines = append(curLines, bulletText)
			}
			continue
		}
		if curRole == "" {
			curRole = RoleAssistant
		}
		curLines = append(curLines, strings.TrimRight(line, " "))
	}
	flush(len(lines))
	return items
}

func isTUIStatusChrome(trimmed string) bool {
	// Horizontal border lines of box-drawing dashes/bars.
	allRule := true
	for _, r := range trimmed {
		if r != '─' && r != '━' && r != '═' && r != '-' && r != ' ' && r != '╭' && r != '╮' && r != '╰' && r != '╯' && r != '│' {
			allRule = false
			break
		}
	}
	if allRule && len([]rune(trimmed)) >= 3 {
		return true
	}
	lower := strings.ToLower(trimmed)
	for _, footer := range []string{
		"bypass permissions on",
		"shift+tab to cycle",
		"? for shortcuts",
		"ctrl+c to interrupt",
	} {
		if strings.Contains(lower, footer) {
			return true
		}
	}
	return false
}

func cutVTPrompt(trimmed string) (string, bool) {
	for _, p := range []string{"❯ ", "❯", "› "} {
		if strings.HasPrefix(trimmed, p) {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, p))
			if rest == "" {
				return "", false
			}
			return rest, true
		}
	}
	for _, sp := range []string{"$ ", "% "} {
		if strings.HasPrefix(trimmed, sp) && len(trimmed) > len(sp) {
			return strings.TrimSpace(trimmed[len(sp):]), true
		}
	}
	return "", false
}

func cutVTAgentMarker(trimmed string) (text string, isSubOutput bool) {
	for _, m := range []string{"⏺ ", "● "} {
		if strings.HasPrefix(trimmed, m) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, m)), false
		}
	}
	if strings.HasPrefix(trimmed, "⎿") {
		return "↳ " + strings.TrimSpace(strings.TrimPrefix(trimmed, "⎿")), true
	}
	return "", false
}

func nonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
