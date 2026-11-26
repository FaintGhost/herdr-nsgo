package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
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
	FormatPi          TranscriptFormat = "pi"
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

// ChatToolCall represents one tool invocation and its paired result summary (plus optional file diff).
type ChatToolCall struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name"`
	Target       string `json:"target,omitempty"`
	InputSummary string `json:"input_summary,omitempty"`
	Output       string `json:"output,omitempty"`
	IsError      bool   `json:"is_error,omitempty"`
	HasResult    bool   `json:"has_result,omitzero"`
	DiffFile     string `json:"diff_file,omitempty"`
	DiffOld      string `json:"diff_old,omitempty"`
	DiffNew      string `json:"diff_new,omitempty"`
	DiffPatch    string `json:"diff_patch,omitempty"`
}

// HasDiff reports whether this tool call carries a structured file modification diff.
func (tc ChatToolCall) HasDiff() bool {
	return tc.DiffPatch != "" || tc.DiffOld != "" || tc.DiffNew != ""
}

// ChatTurnItem is the immutable normalized view-model item rendered by screen_chat.go.
type ChatTurnItem struct {
	ID           string
	Role         ChatRole
	Text         string
	FromAgent    string
	Model        string
	Effort       string
	App          string
	Blocks       []MarkdownBlock
	Tools        []ChatToolCall
	DeliveryMode string `json:",omitempty"`
	Optimistic   bool
	SourceLine   int // 1-based file line number where this item originated (0 for VT/optimistic)
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
	if t.Template != nil && strings.TrimSpace(*t.Template) != "" {
		candidates = append(candidates, *t.Template)
	}
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

func utf8SafePrefix(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

func utf8SafeSuffix(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

func truncateToolOutput(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if s == "" {
		return ""
	}
	if len(s) > 16384 {
		s = utf8SafePrefix(s, 8192) + "\n… [输出超长已裁剪] …\n" + utf8SafeSuffix(s, 8192)
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

// sanitizeOversizedJSONLLine shrinks JSON string literals > 32 KiB (such as embedded
// Base64 screenshots or multi-megabyte tool_result outputs) in a single O(N) pass so
// lines > 512 KiB remain valid JSON and preserve user text, [图片附件], and head/tail outputs.
func sanitizeOversizedJSONLLine(raw string) string {
	if len(raw) <= maxSafeJSONLLineBytes {
		return raw
	}
	const maxLiteralBytes = 32768
	var out strings.Builder
	out.Grow(65536)

	n := len(raw)
	i := 0
	for i < n {
		if raw[i] != '"' {
			out.WriteByte(raw[i])
			i++
			continue
		}
		// Scan to closing unescaped quote.
		start := i
		i++
		for i < n {
			if raw[i] == '\\' {
				i += 2
				continue
			}
			if raw[i] == '"' {
				i++
				break
			}
			i++
		}
		end := i
		if end > n {
			end = n
		}
		litLen := end - start
		// Check if followed by ':' (a JSON object key is never truncated).
		j := end
		for j < n && (raw[j] == ' ' || raw[j] == '\t') {
			j++
		}
		isKey := j < n && raw[j] == ':'
		if isKey || litLen <= maxLiteralBytes {
			out.WriteString(raw[start:end])
			continue
		}

		// Oversized string value: inspect decoded or raw content.
		var decoded string
		if json.Unmarshal([]byte(raw[start:end]), &decoded) != nil {
			out.WriteString(`"[超长内容已省略]"`)
			continue
		}
		if looksLikeBase64Blob(decoded) {
			out.WriteString(`"[base64-omitted]"`)
			continue
		}
		clipped := utf8SafePrefix(decoded, 8192) + "\n… [输出超长已裁剪] …\n" + utf8SafeSuffix(decoded, 8192)
		enc, err := json.Marshal(clipped)
		if err != nil {
			out.WriteString(`"[超长内容已省略]"`)
		} else {
			out.Write(enc)
		}
	}
	return out.String()
}

func looksLikeBase64Blob(s string) bool {
	if len(s) < 4096 {
		return false
	}
	sample := s
	if len(sample) > 512 {
		sample = sample[:512]
	}
	for i := 0; i < len(sample); i++ {
		c := sample[i]
		isB64 := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=' || c == '-' || c == '_'
		if !isB64 {
			return false
		}
	}
	return true
}

var reLooseToolTarget = regexp.MustCompile(`"(?:file_path|path|Path|command|CommandLine|cmd|query)"\s*:\s*"([^"]+)"`)

func extractToolTarget(name string, rawArgs any) (target, summary string) {
	var m map[string]any
	parseStr := func(v string) (string, string, bool) {
		v = strings.TrimSpace(v)
		if v == "" {
			return "", "", true
		}
		if json.Unmarshal([]byte(v), &m) == nil {
			return "", "", false
		}
		if sub := reLooseToolTarget.FindStringSubmatch(v); len(sub) == 2 {
			val := strings.Trim(sub[1], `"' `)
			if strings.HasPrefix(val, "/") {
				b := filepath.Base(val)
				if b != "." && b != "/" {
					val = b
				}
			}
			return singleLineClip(val, 64), truncateToolOutput(v), true
		}
		trimmed := strings.Trim(v, `"' `)
		return singleLineClip(trimmed, 80), truncateToolOutput(v), true
	}

	switch v := rawArgs.(type) {
	case map[string]any:
		m = v
	case string:
		if t, s, done := parseStr(v); done {
			return t, s
		}
	case json.RawMessage:
		if len(v) > 0 {
			if json.Unmarshal(v, &m) != nil {
				var innerStr string
				if json.Unmarshal(v, &innerStr) == nil {
					if t, s, done := parseStr(innerStr); done {
						return t, s
					}
				} else if t, s, done := parseStr(string(v)); done {
					return t, s
				}
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

const maxExtractedDiffBytes = 24 * 1024

// buildToolCall constructs a ChatToolCall with target, summary, and optional file-edit diff extracted from rawArgs.
func buildToolCall(id, name string, rawArgs any) ChatToolCall {
	target, summary := extractToolTarget(name, rawArgs)
	tc := ChatToolCall{
		ID:           id,
		Name:         name,
		Target:       target,
		InputSummary: summary,
	}
	diffFile, diffOld, diffNew, diffPatch := extractToolDiff(name, rawArgs)
	tc.DiffFile = diffFile
	tc.DiffOld = diffOld
	tc.DiffNew = diffNew
	tc.DiffPatch = diffPatch
	if tc.Target == "" && diffFile != "" {
		tc.Target = filepath.Base(diffFile)
	}
	return tc
}

func extractToolDiff(name string, rawArgs any) (file, oldStr, newStr, patch string) {
	lowerName := strings.ToLower(strings.TrimSpace(name))
	if lowerName == "apply_patch" {
		rawPatch := ""
		switch v := rawArgs.(type) {
		case string:
			rawPatch = v
		case json.RawMessage:
			if json.Unmarshal(v, &rawPatch) != nil {
				var pm map[string]any
				if json.Unmarshal(v, &pm) == nil {
					for _, k := range []string{"patch", "input", "diff", "cmd"} {
						if sv, ok := pm[k].(string); ok && sv != "" {
							rawPatch = sv
							break
						}
					}
				}
			}
		case map[string]any:
			for _, k := range []string{"patch", "input", "diff", "cmd"} {
				if sv, ok := v[k].(string); ok && sv != "" {
					rawPatch = sv
					break
				}
			}
		}
		if f, o, n, up := parseApplyPatchPayload(rawPatch); f != "" || up != "" || o != "" || n != "" {
			return f, utf8SafePrefix(o, maxExtractedDiffBytes), utf8SafePrefix(n, maxExtractedDiffBytes), utf8SafePrefix(up, maxExtractedDiffBytes)
		}
	}

	var m map[string]any
	switch v := rawArgs.(type) {
	case map[string]any:
		m = v
	case string:
		_ = json.Unmarshal([]byte(v), &m)
	case json.RawMessage:
		if len(v) > 0 && json.Unmarshal(v, &m) != nil {
			var inner string
			if json.Unmarshal(v, &inner) == nil {
				_ = json.Unmarshal([]byte(inner), &m)
			}
		}
	}
	if len(m) == 0 {
		return "", "", "", ""
	}

	strField := func(keys ...string) (string, bool) {
		for _, k := range keys {
			if val, ok := m[k]; ok {
				if sv, okStr := val.(string); okStr {
					return sv, true
				}
			}
		}
		return "", false
	}

	file, _ = strField("file_path", "Path", "path", "TargetFile", "target_file")

	// 1. Direct old -> new replacement tools (Claude Edit, Antigravity edit_file/replace_file_content, OMP edit).
	oldVal, hasOld := strField("old_string", "Target", "TargetContent", "old_str", "oldText")
	newVal, hasNew := strField("new_string", "Replacement", "ReplacementContent", "new_str", "newText")
	if hasOld || hasNew {
		return file, utf8SafePrefix(oldVal, maxExtractedDiffBytes), utf8SafePrefix(newVal, maxExtractedDiffBytes), ""
	}

	// 2. Multi-hunk edit arrays (MultiEdit / edits / ReplacementChunks).
	for _, arrKey := range []string{"edits", "ReplacementChunks", "chunks"} {
		if rawArr, ok := m[arrKey].([]any); ok && len(rawArr) > 0 {
			var oldParts, newParts []string
			for _, elem := range rawArr {
				em, okMap := elem.(map[string]any)
				if !okMap {
					continue
				}
				var o, n string
				for _, okk := range []string{"old_string", "TargetContent", "Target", "oldText"} {
					if sv, okS := em[okk].(string); okS {
						o = sv
						break
					}
				}
				for _, nkk := range []string{"new_string", "ReplacementContent", "Replacement", "newText"} {
					if sv, okS := em[nkk].(string); okS {
						n = sv
						break
					}
				}
				if o != "" || n != "" {
					oldParts = append(oldParts, o)
					newParts = append(newParts, n)
				}
			}
			if len(oldParts) > 0 || len(newParts) > 0 {
				return file,
					utf8SafePrefix(strings.Join(oldParts, "\n…\n"), maxExtractedDiffBytes),
					utf8SafePrefix(strings.Join(newParts, "\n…\n"), maxExtractedDiffBytes),
					""
			}
		}
	}

	// 3. File write / creation tools (Write / write_file / create_file).
	isWriteTool := lowerName == "write" || lowerName == "write_file" || lowerName == "create_file" || strings.HasSuffix(lowerName, "_write")
	if isWriteTool {
		if content, ok := strField("content", "Content", "CodeContent", "file_text"); ok && content != "" {
			return file, "", utf8SafePrefix(content, maxExtractedDiffBytes), ""
		}
	}

	// 4. Unified diff string field.
	if p, ok := strField("patch", "diff"); ok && strings.Contains(p, "@@") {
		return file, "", "", utf8SafePrefix(p, maxExtractedDiffBytes)
	}
	return "", "", "", ""
}

func parseApplyPatchPayload(raw string) (file, oldStr, newStr, unified string) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if raw == "" {
		return "", "", "", ""
	}
	if strings.Contains(raw, "--- ") && strings.Contains(raw, "+++ ") && strings.Contains(raw, "@@") {
		for _, ln := range strings.Split(raw, "\n") {
			if strings.HasPrefix(ln, "+++ b/") {
				file = strings.TrimSpace(strings.TrimPrefix(ln, "+++ b/"))
				break
			}
		}
		return file, "", "", raw
	}
	if !strings.Contains(raw, "*** ") {
		return "", "", "", ""
	}
	var oldLines, newLines []string
	for _, ln := range strings.Split(raw, "\n") {
		switch {
		case strings.HasPrefix(ln, "*** Update File:"), strings.HasPrefix(ln, "*** Add File:"), strings.HasPrefix(ln, "*** Delete File:"):
			if file == "" {
				if idx := strings.IndexByte(ln, ':'); idx >= 0 {
					file = strings.TrimSpace(ln[idx+1:])
				}
			}
		case strings.HasPrefix(ln, "***"):
			continue
		case strings.HasPrefix(ln, "@@"):
			if len(oldLines) > 0 || len(newLines) > 0 {
				oldLines = append(oldLines, "…")
				newLines = append(newLines, "…")
			}
		case strings.HasPrefix(ln, "-"):
			oldLines = append(oldLines, ln[1:])
		case strings.HasPrefix(ln, "+"):
			newLines = append(newLines, ln[1:])
		case strings.HasPrefix(ln, " "):
			oldLines = append(oldLines, ln[1:])
			newLines = append(newLines, ln[1:])
		}
	}
	return file, strings.Join(oldLines, "\n"), strings.Join(newLines, "\n"), ""
}

func singleLineClip(s string, maxRunes int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) > maxRunes {
		return string(r[:maxRunes]) + "…"
	}
	return s
}

// ParseTranscriptLines dispatches lines (starting at line 1) to the matching Harness JSONL parser.
func ParseTranscriptLines(format TranscriptFormat, lines []string) []ChatTurnItem {
	return ParseTranscriptLinesAt(format, lines, 1)
}

// ParseTranscriptLinesAt dispatches lines starting at 1-based file line firstLine so
// item IDs and SourceLine remain stable across LoadOlder pagination prepends.
func ParseTranscriptLinesAt(format TranscriptFormat, lines []string, firstLine int) []ChatTurnItem {
	if firstLine < 1 {
		firstLine = 1
	}
	switch format {
	case FormatClaude:
		return parseClaudeJSONLAt(lines, firstLine)
	case FormatPi:
		return parsePiOmpJSONLAt(lines, firstLine, "pi")
	case FormatPiOmp:
		return parsePiOmpJSONLAt(lines, firstLine, "omp")
	case FormatCodex:
		return parseCodexJSONLAt(lines, firstLine)
	case FormatAntigravity:
		return parseAntigravityJSONLAt(lines, firstLine)
	default:
		return ParseVTTranscript(strings.Join(lines, "\n"))
	}
}

// appendToolItem appends a ChatToolCall, coalescing adjacent tool calls into one RoleToolGroup.
func appendToolItem(items *[]ChatTurnItem, id string, sourceLine int, tc ChatToolCall, toolIdx map[string][2]int) {
	n := len(*items)
	if n > 0 && (*items)[n-1].Role == RoleToolGroup {
		grpIdx := n - 1
		subIdx := len((*items)[grpIdx].Tools)
		(*items)[grpIdx].Tools = append((*items)[grpIdx].Tools, tc)
		if sourceLine > (*items)[grpIdx].SourceLine {
			(*items)[grpIdx].SourceLine = sourceLine
		}
		if tc.ID != "" {
			toolIdx[tc.ID] = [2]int{grpIdx, subIdx}
		}
		return
	}
	grpIdx := n
	*items = append(*items, ChatTurnItem{
		ID:         id,
		Role:       RoleToolGroup,
		Tools:      []ChatToolCall{tc},
		SourceLine: sourceLine,
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
	items[loc[0]].Tools[loc[1]].HasResult = true
	items[loc[0]].Tools[loc[1]].Output = truncateToolOutput(output)
	if isErr {
		items[loc[0]].Tools[loc[1]].IsError = true
	}
	return true
}

// ---------------------------------------------------------------------------
// 1. Claude Code (~/.claude/projects/*/*.jsonl)

func ParseClaudeJSONL(lines []string) []ChatTurnItem {
	return parseClaudeJSONLAt(lines, 1)
}

func parseClaudeJSONLAt(lines []string, firstLine int) []ChatTurnItem {
	var items []ChatTurnItem
	toolIdx := make(map[string][2]int)
	seenUUID := make(map[string]bool)
	lastAsstMsgID := ""
	curEffort := ""
	baseIdx := firstLine - 1

	for lineNo, raw := range lines {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		raw = sanitizeOversizedJSONLLine(raw)
		absIdx := baseIdx + lineNo
		srcLine := absIdx + 1

		var env struct {
			Type    string `json:"type"`
			UUID    string `json:"uuid"`
			Message struct {
				ID      string          `json:"id"`
				Model   string          `json:"model"`
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
			Params struct {
				Model        string `json:"model"`
				OutputConfig struct {
					Effort string `json:"effort"`
				} `json:"output_config"`
			} `json:"params"`
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
		case "api-request":
			if eff := strings.TrimSpace(env.Params.OutputConfig.Effort); eff != "" {
				curEffort = strings.ToLower(eff)
			}
			continue
		case "user":
			var strContent string
			if json.Unmarshal(env.Message.Content, &strContent) == nil {
				if eff := extractClaudeEffortCommandArg(strContent); eff != "" {
					curEffort = eff
				}
				txt, from := cleanUserPrompt(strContent)
				if txt != "" {
					items = append(items, ChatTurnItem{
						ID:         nonEmpty(env.UUID, fmt.Sprintf("cu-%d", absIdx)),
						Role:       RoleUser,
						Text:       txt,
						FromAgent:  from,
						SourceLine: srcLine,
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
						ID:         nonEmpty(env.UUID, fmt.Sprintf("cu-%d", absIdx)),
						Role:       RoleUser,
						Text:       joined,
						FromAgent:  from,
						SourceLine: srcLine,
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
			msgID := nonEmpty(env.Message.ID, nonEmpty(env.UUID, fmt.Sprintf("ca-%d", absIdx)))
			for bi, b := range blocks {
				switch b.Type {
				case "thinking":
					th := strings.TrimSpace(b.Thinking)
					if th == "" {
						continue
					}
					if len(items) > 0 && items[len(items)-1].Role == RoleThinking && lastAsstMsgID == msgID {
						items[len(items)-1].Text += "\n" + th
						items[len(items)-1].SourceLine = srcLine
					} else {
						items = append(items, ChatTurnItem{
							ID:         fmt.Sprintf("%s-th-%d-%d", msgID, absIdx, bi),
							Role:       RoleThinking,
							Text:       th,
							Model:      env.Message.Model,
							Effort:     curEffort,
							App:        "claude-code",
							SourceLine: srcLine,
						})
					}
				case "tool_use":
					appendToolItem(&items, fmt.Sprintf("%s-tg-%d", msgID, absIdx), srcLine, buildToolCall(b.ID, b.Name, b.Input), toolIdx)
				case "text":
					txt := strings.TrimSpace(b.Text)
					if txt == "" {
						continue
					}
					if len(items) > 0 && items[len(items)-1].Role == RoleAssistant && lastAsstMsgID == msgID {
						merged := items[len(items)-1].Text + "\n\n" + txt
						items[len(items)-1].Text = merged
						items[len(items)-1].Blocks = SplitMarkdownBlocks(merged)
						items[len(items)-1].SourceLine = srcLine
						if curEffort != "" {
							items[len(items)-1].Effort = curEffort
						}
					} else {
						items = append(items, ChatTurnItem{
							ID:         fmt.Sprintf("%s-txt-%d-%d", msgID, absIdx, bi),
							Role:       RoleAssistant,
							Text:       txt,
							Model:      env.Message.Model,
							Effort:     curEffort,
							App:        "claude-code",
							Blocks:     SplitMarkdownBlocks(txt),
							SourceLine: srcLine,
						})
					}
				}
			}
			lastAsstMsgID = msgID
		}
	}
	return items
}

func extractClaudeEffortCommandArg(s string) string {
	if !strings.Contains(s, "<command-name>/effort</command-name>") {
		return ""
	}
	start := strings.Index(s, "<command-args>")
	if start < 0 {
		return ""
	}
	rest := s[start+len("<command-args>"):]
	end := strings.Index(rest, "</command-args>")
	if end < 0 {
		return ""
	}
	arg := strings.ToLower(strings.TrimSpace(rest[:end]))
	if isKnownEffortToken(arg) {
		return arg
	}
	return ""
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
	return parsePiOmpJSONLAt(lines, 1, "omp")
}

func parsePiOmpJSONLAt(lines []string, firstLine int, app string) []ChatTurnItem {
	var items []ChatTurnItem
	toolIdx := make(map[string][2]int)
	baseIdx := firstLine - 1
	curEffort := ""

	for lineNo, raw := range lines {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		raw = sanitizeOversizedJSONLLine(raw)
		absIdx := baseIdx + lineNo
		srcLine := absIdx + 1

		var env struct {
			Type          string `json:"type"`
			ID            string `json:"id"`
			ThinkingLevel string `json:"thinkingLevel"`
			Level         string `json:"level"`
			Message       struct {
				Role       string          `json:"role"`
				Model      string          `json:"model"`
				ToolCallID string          `json:"toolCallId"`
				ToolName   string          `json:"toolName"`
				IsError    bool            `json:"isError"`
				Content    json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(raw), &env) != nil {
			continue
		}
		if env.Type == "thinking_level_change" {
			if lv := nonEmpty(strings.TrimSpace(env.ThinkingLevel), strings.TrimSpace(env.Level)); lv != "" {
				curEffort = lv
			}
			continue
		}
		if env.Type != "message" {
			continue
		}
		rowID := nonEmpty(env.ID, fmt.Sprintf("omp-%d", absIdx))

		switch env.Message.Role {
		case "user":
			txt := extractPiContentText(env.Message.Content)
			cleaned, from := cleanUserPrompt(txt)
			if cleaned != "" {
				items = append(items, ChatTurnItem{
					ID:         rowID,
					Role:       RoleUser,
					Text:       cleaned,
					FromAgent:  from,
					SourceLine: srcLine,
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
						ID:         rowID,
						Role:       RoleAssistant,
						Text:       s,
						Model:      env.Message.Model,
						Effort:     curEffort,
						App:        app,
						Blocks:     SplitMarkdownBlocks(s),
						SourceLine: srcLine,
					})
				}
				continue
			}
			for bi, b := range blocks {
				switch b.Type {
				case "thinking":
					if th := strings.TrimSpace(b.Thinking); th != "" {
						items = append(items, ChatTurnItem{
							ID:         fmt.Sprintf("%s-th-%d", rowID, bi),
							Role:       RoleThinking,
							Text:       th,
							Model:      env.Message.Model,
							Effort:     curEffort,
							App:        app,
							SourceLine: srcLine,
						})
					}
				case "toolCall":
					appendToolItem(&items, fmt.Sprintf("%s-tg-%d", rowID, bi), srcLine, buildToolCall(b.ID, b.Name, b.Arguments), toolIdx)
				case "text":
					if txt := strings.TrimSpace(b.Text); txt != "" {
						items = append(items, ChatTurnItem{
							ID:         fmt.Sprintf("%s-txt-%d", rowID, bi),
							Role:       RoleAssistant,
							Text:       txt,
							Model:      env.Message.Model,
							Effort:     curEffort,
							App:        app,
							Blocks:     SplitMarkdownBlocks(txt),
							SourceLine: srcLine,
						})
					}
				}
			}
		case "toolResult":
			out := extractPiContentText(env.Message.Content)
			if !attachToolResult(items, toolIdx, env.Message.ToolCallID, out, env.Message.IsError) && out != "" {
				appendToolItem(&items, rowID, srcLine, ChatToolCall{
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
	return parseCodexJSONLAt(lines, 1)
}

func parseCodexJSONLAt(lines []string, firstLine int) []ChatTurnItem {
	var items []ChatTurnItem
	toolIdx := make(map[string][2]int)
	curModel := ""
	curEffort := ""
	baseIdx := firstLine - 1

	for lineNo, raw := range lines {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		raw = sanitizeOversizedJSONLLine(raw)
		absIdx := baseIdx + lineNo
		srcLine := absIdx + 1

		var env struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(raw), &env) != nil {
			continue
		}
		if env.Type == "turn_context" {
			var tc struct {
				Model           string `json:"model"`
				Effort          string `json:"effort"`
				ReasoningEffort string `json:"reasoning_effort"`
			}
			if json.Unmarshal(env.Payload, &tc) == nil {
				if tc.Model != "" {
					curModel = tc.Model
				}
				if eff := nonEmpty(strings.TrimSpace(tc.Effort), strings.TrimSpace(tc.ReasoningEffort)); eff != "" {
					curEffort = eff
				}
			}
			continue
		}
		if env.Type != "response_item" {
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
		rowID := nonEmpty(item.ID, fmt.Sprintf("cx-%d", absIdx))

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
						ID:         rowID,
						Role:       RoleUser,
						Text:       cleaned,
						FromAgent:  from,
						SourceLine: srcLine,
					})
				}
			} else if role == "assistant" {
				items = append(items, ChatTurnItem{
					ID:         rowID,
					Role:       RoleAssistant,
					Text:       txt,
					Model:      curModel,
					Effort:     curEffort,
					App:        "codex",
					Blocks:     SplitMarkdownBlocks(txt),
					SourceLine: srcLine,
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
					ID:         rowID,
					Role:       RoleThinking,
					Text:       strings.Join(parts, "\n"),
					Model:      curModel,
					Effort:     curEffort,
					App:        "codex",
					SourceLine: srcLine,
				})
			}
		case "function_call", "custom_tool_call":
			rawArgs := item.Arguments
			if len(rawArgs) == 0 {
				rawArgs = item.Input
			}
			appendToolItem(&items, rowID, srcLine, buildToolCall(item.CallID, item.Name, rawArgs), toolIdx)
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
	return parseAntigravityJSONLAt(lines, 1)
}

func parseAntigravityJSONLAt(lines []string, firstLine int) []ChatTurnItem {
	var items []ChatTurnItem
	var pendingToolRefs [][2]int
	baseIdx := firstLine - 1

	for lineNo, raw := range lines {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		raw = sanitizeOversizedJSONLLine(raw)
		absIdx := baseIdx + lineNo
		srcLine := absIdx + 1

		var row struct {
			StepIndex       int    `json:"step_index"`
			Source          string `json:"source"`
			Type            string `json:"type"`
			Status          string `json:"status"`
			Model           string `json:"model"`
			Effort          string `json:"effort"`
			EffortLevel     string `json:"effort_level"`
			ThinkingLevel   string `json:"thinking_level"`
			ReasoningEffort string `json:"reasoning_effort"`
			Thinking        string `json:"thinking"`
			Content         string `json:"content"`
			ToolCalls       []struct {
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"tool_calls"`
		}
		if json.Unmarshal([]byte(raw), &row) != nil {
			continue
		}
		rowID := fmt.Sprintf("agy-%d-%d", row.StepIndex, absIdx)

		switch row.Type {
		case "USER_INPUT":
			pendingToolRefs = nil
			txt, from := cleanUserPrompt(row.Content)
			if txt != "" {
				items = append(items, ChatTurnItem{
					ID:         rowID,
					Role:       RoleUser,
					Text:       txt,
					FromAgent:  from,
					SourceLine: srcLine,
				})
			}
		case "PLANNER_RESPONSE":
			pendingToolRefs = nil
			cleanModel, modelEffort := splitModelAndEffort(row.Model)
			eff := nonEmpty(
				strings.TrimSpace(row.Effort),
				nonEmpty(
					strings.TrimSpace(row.EffortLevel),
					nonEmpty(
						strings.TrimSpace(row.ThinkingLevel),
						nonEmpty(strings.TrimSpace(row.ReasoningEffort), modelEffort),
					),
				),
			)
			if th := strings.TrimSpace(row.Thinking); th != "" {
				items = append(items, ChatTurnItem{
					ID:         rowID + "-th",
					Role:       RoleThinking,
					Text:       th,
					Model:      cleanModel,
					Effort:     eff,
					App:        "antigravity",
					SourceLine: srcLine,
				})
			}
			if txt := strings.TrimSpace(row.Content); txt != "" {
				items = append(items, ChatTurnItem{
					ID:         rowID + "-txt",
					Role:       RoleAssistant,
					Text:       txt,
					Model:      cleanModel,
					Effort:     eff,
					App:        "antigravity",
					Blocks:     SplitMarkdownBlocks(txt),
					SourceLine: srcLine,
				})
			}
			if len(row.ToolCalls) > 0 {
				dummyMap := make(map[string][2]int)
				for ti, tc := range row.ToolCalls {
					callKey := fmt.Sprintf("%s-tc-%d", rowID, ti)
					appendToolItem(&items, rowID+"-tg", srcLine, buildToolCall(callKey, tc.Name, tc.Args), dummyMap)
					pendingToolRefs = append(pendingToolRefs, dummyMap[callKey])
				}
			}
		case "GENERIC":
			if row.Source == "MODEL" && len(pendingToolRefs) > 0 {
				loc := pendingToolRefs[0]
				pendingToolRefs = pendingToolRefs[1:]
				if loc[0] < len(items) && loc[1] < len(items[loc[0]].Tools) {
					items[loc[0]].Tools[loc[1]].HasResult = true
					items[loc[0]].Tools[loc[1]].Output = truncateToolOutput(row.Content)
					stUpper := strings.ToUpper(strings.TrimSpace(row.Status))
					failedExit := strings.Contains(row.Content, "Command exited with code ") && !strings.Contains(row.Content, "Command exited with code 0")
					if stUpper == "ERROR" || stUpper == "FAILED" || failedExit {
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

// ContextUsage summarizes the most recent turn's token footprint against the estimated model context window.
type ContextUsage struct {
	ContextTokens int
	OutputTokens  int
	WindowTokens  int
	Model         string
}

// Valid reports whether meaningful token usage data was found.
func (u ContextUsage) Valid() bool {
	return u.ContextTokens > 0
}

// Ratio returns context utilization in [0, 1+].
func (u ContextUsage) Ratio() float64 {
	if u.ContextTokens <= 0 || u.WindowTokens <= 0 {
		return 0
	}
	return float64(u.ContextTokens) / float64(u.WindowTokens)
}

// Label returns a compact human-readable badge like "64k · 32%".
func (u ContextUsage) Label() string {
	if u.ContextTokens <= 0 {
		return ""
	}
	toks := formatCompactTokens(u.ContextTokens)
	if u.WindowTokens <= 0 {
		return toks
	}
	pct := int(u.Ratio()*100 + 0.5)
	if pct > 100 {
		pct = 100
	}
	return fmt.Sprintf("%s · %d%%", toks, pct)
}

func formatCompactTokens(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1000 {
		k := float64(n) / 1000
		if k >= 10 {
			return fmt.Sprintf("%.0fk", k)
		}
		return fmt.Sprintf("%.1fk", k)
	}
	return fmt.Sprintf("%d", n)
}

func estimateContextWindow(model string, used int) int {
	lower := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(lower, "gemini"):
		return 1_000_000
	case strings.Contains(lower, "claude"):
		if used > 200_000 {
			return 1_000_000
		}
		return 200_000
	case strings.Contains(lower, "gpt-5") || strings.Contains(lower, "codex"):
		if used > 200_000 {
			return 400_000
		}
		return 200_000
	default:
		if used > 200_000 {
			return 1_000_000
		}
		return 200_000
	}
}

// ParseTranscriptUsage scans JSONL lines in reverse to extract the latest context token footprint in O(few lines).
func ParseTranscriptUsage(format TranscriptFormat, lines []string) ContextUsage {
	for i := len(lines) - 1; i >= 0; i-- {
		raw := strings.TrimSpace(lines[i])
		if raw == "" {
			continue
		}
		switch format {
		case FormatClaude:
			if !strings.Contains(raw, `"usage"`) {
				continue
			}
			var env struct {
				Message struct {
					Model string `json:"model"`
					Usage struct {
						InputTokens              int `json:"input_tokens"`
						CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
						CacheReadInputTokens     int `json:"cache_read_input_tokens"`
						OutputTokens             int `json:"output_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(raw), &env) != nil {
				continue
			}
			ctxToks := env.Message.Usage.InputTokens + env.Message.Usage.CacheCreationInputTokens + env.Message.Usage.CacheReadInputTokens
			if ctxToks > 0 {
				return ContextUsage{
					ContextTokens: ctxToks,
					OutputTokens:  env.Message.Usage.OutputTokens,
					WindowTokens:  estimateContextWindow(env.Message.Model, ctxToks),
					Model:         env.Message.Model,
				}
			}

		case FormatPi, FormatPiOmp:
			if !strings.Contains(raw, `"usage"`) {
				continue
			}
			var env struct {
				Message struct {
					Model string `json:"model"`
					Usage struct {
						Input       int `json:"input"`
						CacheRead   int `json:"cacheRead"`
						CacheWrite  int `json:"cacheWrite"`
						Output      int `json:"output"`
						TotalTokens int `json:"totalTokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(raw), &env) != nil {
				continue
			}
			ctxToks := env.Message.Usage.Input + env.Message.Usage.CacheRead + env.Message.Usage.CacheWrite
			if ctxToks <= 0 {
				ctxToks = env.Message.Usage.TotalTokens
			}
			if ctxToks > 0 {
				return ContextUsage{
					ContextTokens: ctxToks,
					OutputTokens:  env.Message.Usage.Output,
					WindowTokens:  estimateContextWindow(env.Message.Model, ctxToks),
					Model:         env.Message.Model,
				}
			}

		case FormatCodex:
			if !strings.Contains(raw, `"token_count"`) {
				continue
			}
			var env struct {
				Type    string `json:"type"`
				Payload struct {
					Type string `json:"type"`
					Info *struct {
						ModelContextWindow int `json:"model_context_window"`
						LastTokenUsage     struct {
							InputTokens  int `json:"input_tokens"`
							OutputTokens int `json:"output_tokens"`
							TotalTokens  int `json:"total_tokens"`
						} `json:"last_token_usage"`
					} `json:"info"`
				} `json:"payload"`
			}
			if json.Unmarshal([]byte(raw), &env) != nil || env.Payload.Info == nil {
				continue
			}
			u := env.Payload.Info.LastTokenUsage
			ctxToks := u.InputTokens
			if ctxToks <= 0 {
				ctxToks = u.TotalTokens
			}
			if ctxToks > 0 {
				win := env.Payload.Info.ModelContextWindow
				if win <= 0 {
					win = estimateContextWindow("codex", ctxToks)
				}
				return ContextUsage{
					ContextTokens: ctxToks,
					OutputTokens:  u.OutputTokens,
					WindowTokens:  win,
					Model:         "codex",
				}
			}
		}
	}
	return ContextUsage{}
}
