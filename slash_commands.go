package main

import (
	"encoding/json"
	"encoding/xml"
	"slices"
	"strings"
	"unicode"

	"github.com/nsgo-dev/nsgo/core"
)

// Names are entered literally. Pi skills come from the active transcript's resource manifest.
var chatCommandNames = map[string][]string{
	"claude":      {"help", "compact", "model", "status", "clear", "context", "cost", "memory", "resume", "config", "permissions", "skills", "mcp", "usage", "doctor", "diff", "init", "agents", "add-dir", "btw", "copy", "effort", "export", "fast", "fork", "hooks", "ide", "login", "logout", "plan", "plugin", "reload-skills", "rename", "rewind", "sandbox", "stats", "tasks", "theme", "vim"},
	"codex":       {"model", "status", "diff", "compact", "new", "resume", "fork", "review", "permissions", "skills", "mcp", "init", "mention", "exit", "agents", "apps", "archive", "clear", "config", "copy", "export", "feedback", "hooks", "keymap", "logout", "plan", "plugins", "ps", "pwd", "rename", "statusline", "stop", "theme", "usage"},
	"pi":          {"model", "settings", "compact", "tree", "resume", "new", "thinking", "scoped-models", "export", "import", "share", "bug", "copy", "name", "session", "changelog", "hotkeys", "fork", "clone", "trust", "login", "logout", "reload", "quit"},
	"omp":         {"model", "settings", "compact", "resume", "new"},
	"antigravity": {"help", "model"},
}

// Short descriptions for built-ins shared by the supported agents. Discovered entries carry their own descriptions.
var slashCommandHints = map[string]string{
	"help": "查看帮助", "model": "切换模型", "compact": "压缩上下文", "status": "会话状态",
	"clear": "清空上下文", "context": "上下文用量", "resume": "继续已有会话", "settings": "会话设置",
	"thinking": "思考强度", "effort": "思考强度", "new": "新建会话", "diff": "查看改动",
	"tree": "浏览会话分支", "skills": "查看技能", "permissions": "权限设置", "fork": "从此处分支",
}

type slashEntry struct {
	Name        string `xml:"name"`
	Description string `xml:"description"`
}

// A nil result means the window contains no skill manifest. An empty list clears older skills.
func piSkillManifest(lines []string) []slashEntry {
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if !strings.Contains(line, "available_skills") {
			continue
		}
		var entry struct {
			Type    string
			Message struct {
				Role     string
				Content  string
				Sections struct{ Skills string }
			}
		}
		if json.Unmarshal([]byte(line), &entry) != nil || entry.Type != "message" || entry.Message.Role != "system" {
			continue
		}
		source := entry.Message.Sections.Skills
		if source == "" {
			source = entry.Message.Content
		}
		_, rest, ok := strings.Cut(source, "<available_skills>")
		if !ok {
			continue
		}
		body, _, ok := strings.Cut(rest, "</available_skills>")
		if !ok {
			continue
		}
		var manifest struct {
			Skills []slashEntry `xml:"skill"`
		}
		if xml.Unmarshal([]byte("<available_skills>"+body+"</available_skills>"), &manifest) != nil {
			continue
		}
		out := make([]slashEntry, 0, len(manifest.Skills))
		seen := map[string]bool{}
		for _, skill := range manifest.Skills {
			name := strings.TrimSpace(skill.Name)
			if name == "" || strings.IndexFunc(name, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 || seen[name] {
				continue
			}
			seen[name] = true
			skill.Name = "skill:" + name
			skill.Description = strings.Join(strings.Fields(skill.Description), " ")
			out = append(out, skill)
		}
		slices.SortFunc(out, func(a, b slashEntry) int { return strings.Compare(a.Name, b.Name) })
		return out
	}
	return nil
}

func slashCommandBar(cx *core.Scope, a *App, draft func() string, term func() AggregatedTerminal, controller func() *ChatSessionController, enabled func() bool) core.Node {
	open := core.NewMemo(cx, func() bool {
		return enabled() && (strings.HasPrefix(draft(), "/") || (strings.HasPrefix(draft(), "$") && DetectHarnessKind(term().Terminal) == "codex"))
	})
	cx.Effect(func() {
		if open.Get() {
			if c := controller(); c != nil {
				t := term().Terminal
				c.discoverCommands(DetectHarnessKind(t), t.CurrentDir())
			}
		}
	})
	matches := core.NewMemoFunc(cx, func() []slashEntry {
		if !enabled() {
			return nil
		}
		query, ok := strings.CutPrefix(draft(), "/")
		if !ok && DetectHarnessKind(term().Terminal) == "codex" && strings.HasPrefix(draft(), "$") {
			query = draft()
			ok = true
		}
		if !ok || strings.IndexFunc(query, unicode.IsSpace) >= 0 {
			return nil
		}
		query = strings.ToLower(query)
		kind := DetectHarnessKind(term().Terminal)
		var found []slashEntry
		for _, name := range chatCommandNames[kind] {
			if strings.HasPrefix(name, query) {
				found = append(found, slashEntry{Name: name, Description: slashCommandHints[name]})
			}
		}
		if c := controller(); c != nil {
			seen := make(map[string]bool, len(found))
			for _, e := range found {
				seen[e.Name] = true
			}
			for _, e := range c.commandEntries(kind) {
				if !seen[e.Name] && strings.Contains(strings.ToLower(e.Name), query) {
					found = append(found, e)
					seen[e.Name] = true
				}
			}
		}
		return found
	}, slices.Equal[[]slashEntry])
	return core.Show{When: func() bool { return len(matches.Get()) > 0 }, Then: func(cx *core.Scope) core.Node {
		const rowHeight = 44
		return core.Scroll{TestID: "slash-command-list", StyleFunc: func() core.Style {
			pal := a.paletteOf(cx)
			available := cx.Env().Window().Height - cx.Env().KeyboardHeight()
			return core.Style{
				Height:       core.Pt(min(core.Points(rowHeight*len(matches.Get())), min(210, max(rowHeight, available/3)))),
				MinWidth:     core.Pt(0),
				Background:   pal.Surface,
				CornerRadius: Tokens.RadiusCtrl,
				BorderWidth:  1,
				BorderColor:  pal.Border,
			}
		}, Children: []core.Node{core.Each(matches.Get, func(e slashEntry) string { return e.Name }, func(cx *core.Scope, item func() slashEntry, idx func() int) core.Node {
			entry := item()
			label := "/" + entry.Name
			if strings.HasPrefix(entry.Name, "$") {
				label = entry.Name
			}
			hasLongDescription := strings.HasPrefix(entry.Name, "skill:") || strings.HasPrefix(entry.Name, "$") || len([]rune(entry.Description)) > 12
			style := core.Style{Height: core.Pt(rowHeight), FlexShrink: 0, Direction: core.Row, AlignItems: core.AlignCenter, Gap: Tokens.Space2, Padding: core.Symmetric(0, Tokens.Space3)}
			if hasLongDescription {
				style.Direction, style.AlignItems, style.JustifyContent, style.Gap = core.Column, core.AlignStretch, core.JustifyCenter, 2
			}
			return core.View{TestID: "slash-command-" + entry.Name, Role: core.RoleButton, Label: label, Style: style,
				StyleFunc: func() core.Style {
					if idx() == 0 {
						return core.Style{}
					}
					return core.Style{Borders: core.Edges{Top: 1}, BorderColor: a.paletteOf(cx).Separator}
				},
				OnPress: func(core.PressEvent) { a.SetCompose(term().Key.String(), label+" ") }, Children: []core.Node{
					core.Text{Content: label, MaxLines: 1, StyleFunc: func() core.Style {
						return core.Style{MinWidth: core.Pt(0), FlexShrink: 1, FontSize: 14, FontFamily: core.FontMonospace, Color: a.paletteOf(cx).Text}
					}},
					core.Show{When: func() bool { return item().Description != "" }, Then: func(cx *core.Scope) core.Node {
						return core.Text{ContentFunc: func() string { return item().Description }, MaxLines: 1, StyleFunc: func() core.Style {
							st := core.Style{MinWidth: core.Pt(0), FlexShrink: 1, FontSize: Tokens.FontMonoMeta, Color: a.paletteOf(cx).TextMuted}
							if !hasLongDescription {
								st.FlexGrow, st.TextAlign = 1, core.TextAlignRight
							}
							return st
						}}
					}},
				}}
		})}}
	}}
}
