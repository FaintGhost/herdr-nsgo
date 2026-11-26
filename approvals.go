package main

import "strings"

// ApprovalAction identifies an inline approval adapter's key sequences.
type ApprovalAction struct {
	AllowKeys []string
	DenyKeys  []string
}

// verifiedApprovalAdapters maps normalized Status.App names to conservative key sequences
// for OSC 7501 blocked+permission dialogs. Unlisted apps MUST degrade to "进终端处理".
var verifiedApprovalAdapters = map[string]ApprovalAction{
	"claude":          {AllowKeys: []string{"enter"}, DenyKeys: []string{"esc"}},
	"claude-code":     {AllowKeys: []string{"enter"}, DenyKeys: []string{"esc"}},
	"codex":           {AllowKeys: []string{"enter"}, DenyKeys: []string{"esc"}},
	"pi":              {AllowKeys: []string{"enter"}, DenyKeys: []string{"esc"}},
	"omp":             {AllowKeys: []string{"enter"}, DenyKeys: []string{"esc"}},
	"antigravity":     {AllowKeys: []string{"enter"}, DenyKeys: []string{"esc"}},
	"antigravity-cli": {AllowKeys: []string{"enter"}, DenyKeys: []string{"esc"}},
	"agy":             {AllowKeys: []string{"enter"}, DenyKeys: []string{"esc"}},
}

func approvalAdapterKey(t Terminal) string {
	if t.Status == nil {
		return ""
	}
	if app := strings.ToLower(strings.TrimSpace(t.Status.App)); app != "" {
		return app
	}
	return DetectHarnessKind(t)
}

// CanInlineApprove reports whether t is eligible for one-tap inline Approve/Deny buttons:
// strictly blocked + permission + verified Status.App whitelist.
func CanInlineApprove(t Terminal) bool {
	if t.StateWord() != "blocked" || t.Status == nil {
		return false
	}
	if t.Status.Kind != "permission" {
		return false
	}
	_, ok := verifiedApprovalAdapters[approvalAdapterKey(t)]
	return ok
}

// ApproveKeys returns the verified key sequence for allow/deny, or (_, false) if not whitelisted.
func ApproveKeys(t Terminal, allow bool) ([]string, bool) {
	if !CanInlineApprove(t) {
		return nil, false
	}
	act := verifiedApprovalAdapters[approvalAdapterKey(t)]
	if allow {
		return append([]string(nil), act.AllowKeys...), true
	}
	return append([]string(nil), act.DenyKeys...), true
}
