package main

import (
	"path/filepath"
	"strings"

	"github.com/nsgo-dev/nsgo/base/textview"
	"github.com/nsgo-dev/nsgo/base/textview/highlight"
	"github.com/nsgo-dev/nsgo/core"
)

// newAppHighlighter wraps NSGo's chroma-backed highlighter with language alias normalization,
// path-suffix stripping (e.g. "go:main.go"), and heuristic detection for unlabeled code fences.
func newAppHighlighter(cx *core.Scope) textview.Highlighter {
	baseHL := highlight.New(cx)
	return func(c textview.CodeBlock) []textview.Highlight {
		if strings.TrimSpace(c.Code) == "" {
			return nil
		}
		lang := normalizeCodeLanguage(c.Language, c.Code)
		if lang == "" {
			return nil
		}
		c.Language = lang
		return baseHL(c)
	}
}

// normalizeCodeLanguage maps fence info strings, file extensions, and aliases to Chroma lexer names.
func normalizeCodeLanguage(rawLang, code string) string {
	lang := strings.TrimSpace(rawLang)
	if lang != "" {
		// Strip trailing metadata ("ts title=...") or ":path" ("go:main.go").
		if idx := strings.IndexAny(lang, " \t{"); idx >= 0 {
			lang = lang[:idx]
		}
		if idx := strings.IndexByte(lang, ':'); idx > 0 {
			prefix := strings.TrimSpace(lang[:idx])
			suffix := strings.TrimSpace(lang[idx+1:])
			if mapped := canonicalLangToken(prefix); mapped != "" {
				return mapped
			}
			if mapped := langFromFilePath(suffix); mapped != "" {
				return mapped
			}
		}
		if mapped := canonicalLangToken(lang); mapped != "" {
			return mapped
		}
		if mapped := langFromFilePath(lang); mapped != "" {
			return mapped
		}
		return strings.ToLower(lang)
	}
	return inferCodeLanguage(code)
}

func canonicalLangToken(tok string) string {
	s := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(tok), "."))
	switch s {
	case "":
		return ""
	case "go", "golang":
		return "go"
	case "ts", "typescript":
		return "typescript"
	case "tsx":
		return "tsx"
	case "js", "javascript", "mjs", "cjs":
		return "javascript"
	case "jsx":
		return "jsx"
	case "py", "python", "python3":
		return "python"
	case "rs", "rust":
		return "rust"
	case "sh", "bash", "zsh", "shell", "console", "shellsession":
		return "bash"
	case "yml", "yaml":
		return "yaml"
	case "json", "jsonc", "jsonl":
		return "json"
	case "toml":
		return "toml"
	case "md", "markdown":
		return "markdown"
	case "sql":
		return "sql"
	case "html", "htm":
		return "html"
	case "css":
		return "css"
	case "scss":
		return "scss"
	case "xml", "svg", "plist":
		return "xml"
	case "diff", "patch":
		return "diff"
	case "swift":
		return "swift"
	case "kt", "kts", "kotlin":
		return "kotlin"
	case "java":
		return "java"
	case "rb", "ruby":
		return "ruby"
	case "php":
		return "php"
	case "c", "h":
		return "c"
	case "cc", "cpp", "cxx", "hpp", "hh", "c++":
		return "cpp"
	case "cs", "csharp":
		return "csharp"
	case "dockerfile", "docker":
		return "docker"
	case "makefile", "mk", "make":
		return "makefile"
	case "proto", "protobuf":
		return "protobuf"
	case "graphql", "gql":
		return "graphql"
	case "lua":
		return "lua"
	case "zig":
		return "zig"
	case "nix":
		return "nix"
	case "hcl", "tf", "terraform":
		return "hcl"
	case "ini", "conf", "cfg", "env":
		return "ini"
	}
	return ""
}

// langFromFilePath infers a Chroma lexer language from a file path or target summary.
func langFromFilePath(p string) string {
	s := strings.TrimSpace(p)
	if s == "" {
		return ""
	}
	// Strip line/column suffixes like "app.go:42" or "app.go#L10-L20".
	if idx := strings.IndexAny(s, "#"); idx > 0 {
		s = s[:idx]
	}
	if idx := strings.IndexByte(s, ' '); idx > 0 {
		s = s[:idx]
	}
	if idx := strings.LastIndexByte(s, ':'); idx > 0 && idx < len(s)-1 {
		tail := s[idx+1:]
		allDigits := true
		for _, r := range tail {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			s = s[:idx]
		}
	}
	base := strings.ToLower(filepath.Base(s))
	switch base {
	case "dockerfile":
		return "docker"
	case "makefile", "gnumakefile":
		return "makefile"
	case "go.mod", "go.sum":
		return "go"
	}
	ext := filepath.Ext(base)
	if ext != "" {
		return canonicalLangToken(ext)
	}
	return ""
}

// toolOutputLanguage determines the best syntax highlighting language for a tool call's output.
func toolOutputLanguage(tc ChatToolCall) string {
	if lang := langFromFilePath(tc.Target); lang != "" {
		return lang
	}
	if lang := langFromFilePath(tc.DiffFile); lang != "" {
		return lang
	}
	lower := strings.ToLower(strings.TrimSpace(tc.Name))
	switch {
	case strings.Contains(lower, "bash") || strings.Contains(lower, "shell") || strings.Contains(lower, "command") || lower == "run":
		return "bash"
	case strings.Contains(lower, "read") || strings.Contains(lower, "write") || strings.Contains(lower, "view"):
		return inferCodeLanguage(tc.Output)
	}
	return ""
}

// inferCodeLanguage performs a fast, zero-allocation-friendly heuristic for unlabeled fenced code blocks.
func inferCodeLanguage(code string) string {
	s := strings.TrimSpace(code)
	if len(s) < 8 {
		return ""
	}
	if strings.HasPrefix(s, "diff --git ") || (strings.Contains(s, "\n@@ ") && (strings.Contains(s, "\n+++ ") || strings.Contains(s, "\n--- "))) {
		return "diff"
	}
	if strings.HasPrefix(s, "#!/") {
		firstLine, _, _ := strings.Cut(s, "\n")
		if strings.Contains(firstLine, "python") {
			return "python"
		}
		if strings.Contains(firstLine, "node") {
			return "javascript"
		}
		return "bash"
	}
	if (strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")) || (strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]")) {
		if strings.Contains(s, `":`) || strings.Contains(s, `" :`) {
			return "json"
		}
	}
	if strings.Contains(s, "package ") && (strings.Contains(s, "\nfunc ") || strings.HasPrefix(s, "package ")) {
		return "go"
	}
	if strings.Contains(s, "func ") && (strings.Contains(s, " := ") || strings.Contains(s, "err != nil") || strings.Contains(s, "return ")) {
		return "go"
	}
	if (strings.Contains(s, "interface ") || strings.Contains(s, ": string") || strings.Contains(s, ": number")) &&
		(strings.Contains(s, "const ") || strings.Contains(s, "export ") || strings.Contains(s, "function ")) {
		return "typescript"
	}
	if (strings.Contains(s, "const ") || strings.Contains(s, "function ") || strings.Contains(s, "=> {")) &&
		(strings.Contains(s, "import ") || strings.Contains(s, "export ") || strings.Contains(s, "return ")) {
		return "javascript"
	}
	if (strings.Contains(s, "def ") || strings.Contains(s, "async def ")) && strings.Contains(s, ":\n") {
		return "python"
	}
	if strings.Contains(s, "fn ") && (strings.Contains(s, "let mut ") || strings.Contains(s, " -> ") || strings.Contains(s, "impl ")) {
		return "rust"
	}
	if strings.HasPrefix(s, "<") && (strings.Contains(s, "</") || strings.Contains(s, "/>")) {
		return "html"
	}
	upper := strings.ToUpper(s)
	if strings.HasPrefix(upper, "SELECT ") || strings.HasPrefix(upper, "CREATE TABLE") || strings.HasPrefix(upper, "INSERT INTO") {
		return "sql"
	}
	return ""
}

// fencedCodeMarkdown wraps raw source text in a Markdown code fence safely longer than any inner backtick run.
func fencedCodeMarkdown(lang, code string) string {
	fence := "```"
	for strings.Contains(code, fence) {
		fence += "`"
	}
	return fence + lang + "\n" + strings.TrimRight(code, "\n") + "\n" + fence
}
