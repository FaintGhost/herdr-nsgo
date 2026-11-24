package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseAllFourHarnessesAndVTFallback(t *testing.T) {
	t.Run("Claude incremental same message.id + tool_result + hc report", func(t *testing.T) {
		lines := []string{
			`{"type":"mode","mode":"default","sessionId":"s1"}`,
			`{"type":"user","uuid":"u1","message":{"role":"user","content":"<system-reminder>ignore me</system-reminder>\n请帮我检查 vt_view.go"}}`,
			`{"type":"assistant","uuid":"a1","message":{"id":"msg_01","model":"claude-opus-4-7","role":"assistant","content":[{"type":"thinking","thinking":"先读取 vt_view.go 文件结构"}]}}`,
			`{"type":"assistant","uuid":"a2","message":{"id":"msg_01","model":"claude-opus-4-7","role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/repo/vt_view.go"}}]}}`,
			`{"type":"user","uuid":"u2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"1: package main\n2: // ok","is_error":false}]}}`,
			`{"type":"assistant","uuid":"a3","message":{"id":"msg_01","model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"已检查完毕，代码如下：\n` + "```go\\nfunc ok() {}\\n```" + `"}]}}`,
			`{"type":"user","uuid":"u3","message":{"role":"user","content":"<hc from=\"t7 review\">Two bugs fixed in x.rs</hc>"}}`,
		}
		items := ParseClaudeJSONL(lines)
		if len(items) < 5 {
			t.Fatalf("expected >= 5 items (user, thinking, toolGroup, assistant text, hc user), got %d: %+v", len(items), items)
		}
		if items[0].Role != RoleUser || strings.Contains(items[0].Text, "system-reminder") || !strings.Contains(items[0].Text, "vt_view.go") {
			t.Fatalf("unexpected user item[0]: %+v", items[0])
		}
		var sawThinking, sawTool, sawAssistant, sawHC bool
		for _, it := range items {
			switch it.Role {
			case RoleThinking:
				if strings.Contains(it.Text, "先读取 vt_view.go") {
					sawThinking = true
				}
			case RoleToolGroup:
				if len(it.Tools) == 1 && it.Tools[0].Name == "Read" && strings.Contains(it.Tools[0].Target, "vt_view.go") && strings.Contains(it.Tools[0].Output, "package main") && !it.Tools[0].IsError {
					sawTool = true
				}
			case RoleAssistant:
				if it.Model == "claude-opus-4-7" && len(it.Blocks) == 2 && it.Blocks[1].Kind == BlockCode && it.Blocks[1].Lang == "go" {
					sawAssistant = true
				}
			case RoleUser:
				if it.FromAgent == "t7 review" && strings.Contains(it.Text, "Two bugs fixed") {
					sawHC = true
				}
			}
		}
		if !sawThinking || !sawTool || !sawAssistant || !sawHC {
			t.Fatalf("missing parsed elements: thinking=%v tool=%v assistant=%v hc=%v items=%+v", sawThinking, sawTool, sawAssistant, sawHC, items)
		}
	})

	t.Run("OMP and Pi JSONL format with toolResult error", func(t *testing.T) {
		lines := []string{
			`{"type":"title","title":"AUTO"}`,
			`{"type":"message","id":"m1","message":{"role":"user","content":[{"type":"text","text":"跑一下单测"},{"type":"image","data":"AAAA"}]}}`,
			`{"type":"message","id":"m2","message":{"role":"assistant","model":"gpt-5.4","content":[{"type":"thinking","thinking":"准备执行测试"},{"type":"toolCall","id":"call_9","name":"bash","arguments":{"command":"go test ./..."}}]}}`,
			`{"type":"message","id":"m3","message":{"role":"toolResult","toolCallId":"call_9","toolName":"bash","isError":true,"content":[{"type":"text","text":"FAIL: TestFoo"}]}}`,
			`{"type":"message","id":"m4","message":{"role":"assistant","model":"gpt-5.4","content":[{"type":"text","text":"单测 ` + "`TestFoo`" + ` 失败了，正在修复。"}]}}`,
		}
		items := ParsePiOmpJSONL(lines)
		var sawImgPlaceholder, sawErrTool, sawReply bool
		for _, it := range items {
			if it.Role == RoleUser && strings.Contains(it.Text, "[图片附件]") {
				sawImgPlaceholder = true
			}
			if it.Role == RoleToolGroup && len(it.Tools) == 1 && it.Tools[0].IsError && strings.Contains(it.Tools[0].Target, "go test") && strings.Contains(it.Tools[0].Output, "FAIL: TestFoo") {
				sawErrTool = true
			}
			if it.Role == RoleAssistant && it.Model == "gpt-5.4" && strings.Contains(it.Text, "TestFoo") {
				sawReply = true
			}
		}
		if !sawImgPlaceholder || !sawErrTool || !sawReply {
			t.Fatalf("OMP parse mismatch: img=%v errTool=%v reply=%v items=%+v", sawImgPlaceholder, sawErrTool, sawReply, items)
		}
	})

	t.Run("Codex response_item stream deduplicating event_msg", func(t *testing.T) {
		lines := []string{
			`{"type":"session_meta","payload":{"session_id":"c1","cwd":"/srv/app"}}`,
			`{"type":"turn_context","payload":{"model":"gpt-5.3-codex"}}`,
			`{"type":"response_item","payload":{"type":"message","id":"msg_u","role":"user","content":[{"type":"input_text","text":"看看状态"}]}}`,
			`{"type":"event_msg","payload":{"type":"item_completed","item":{"role":"user","text":"看看状态"}}}`,
			`{"type":"response_item","payload":{"type":"reasoning","id":"r1","summary":[{"type":"summary_text","text":"检查 git status"}]}}`,
			`{"type":"response_item","payload":{"type":"function_call","id":"fc1","call_id":"call_1","name":"shell","arguments":"{\"command\":[\"git\",\"status\"]}"}}`,
			`{"type":"response_item","payload":{"type":"function_call_output","id":"fco1","call_id":"call_1","output":"On branch main\nnothing to commit"}}`,
			`{"type":"response_item","payload":{"type":"message","id":"msg_a","role":"assistant","content":[{"type":"output_text","text":"当前工作区很干净。"}]}}`,
			`{"type":"event_msg","payload":{"type":"task_complete","last_agent_message":"当前工作区很干净。"}}`,
		}
		items := ParseCodexJSONL(lines)
		userCount, asstCount, toolCount := 0, 0, 0
		for _, it := range items {
			switch it.Role {
			case RoleUser:
				userCount++
			case RoleAssistant:
				asstCount++
				if it.Model != "gpt-5.3-codex" {
					t.Errorf("expected codex model gpt-5.3-codex, got %q", it.Model)
				}
			case RoleToolGroup:
				toolCount += len(it.Tools)
				if !strings.Contains(it.Tools[0].Output, "On branch main") {
					t.Errorf("expected paired output, got %+v", it.Tools[0])
				}
			}
		}
		if userCount != 1 || asstCount != 1 || toolCount != 1 {
			t.Fatalf("expected 1 user, 1 assistant, 1 tool (no event_msg duplicates), got u=%d a=%d t=%d items=%+v", userCount, asstCount, toolCount, items)
		}
	})

	t.Run("Antigravity USER_INPUT + PLANNER_RESPONSE + sequential GENERIC tool outputs", func(t *testing.T) {
		lines := []string{
			`{"step_index":0,"source":"USER","type":"USER_INPUT","content":"<USER_REQUEST>\n实现 Chat View\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\ntime\n</ADDITIONAL_METADATA>"}`,
			`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","model":"gemini-3.1-pro","thinking":"分析需求","tool_calls":[{"name":"read_file","args":{"Path":"/repo/proto.go"}}]}`,
			`{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","content":"1: package main"}`,
			`{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","model":"gemini-3.1-pro","thinking":"开始编码","content":"方案已就绪！"}`,
		}
		items := ParseAntigravityJSONL(lines)
		if len(items) < 4 {
			t.Fatalf("expected >= 4 items, got %d: %+v", len(items), items)
		}
		if items[0].Role != RoleUser || items[0].Text != "实现 Chat View" {
			t.Fatalf("expected stripped USER_REQUEST, got %+v", items[0])
		}
		var sawPairedRead, sawFinalText bool
		for _, it := range items {
			if it.Role == RoleToolGroup && len(it.Tools) == 1 && it.Tools[0].Name == "read_file" && strings.Contains(it.Tools[0].Target, "proto.go") && strings.Contains(it.Tools[0].Output, "package main") {
				sawPairedRead = true
			}
			if it.Role == RoleAssistant && it.Text == "方案已就绪！" {
				sawFinalText = true
			}
		}
		if !sawPairedRead || !sawFinalText {
			t.Fatalf("antigravity parse mismatch: tool=%v final=%v items=%+v", sawPairedRead, sawFinalText, items)
		}
	})

	t.Run("VT Fallback parser strips 140-col status chrome and splits turns", func(t *testing.T) {
		raw := strings.Join([]string{
			"──────────────────────────────────────────────────────────",
			"❯ 帮我看一下报错原因",
			"● Read(vt_view.go)",
			"  ⎿  Read 120 lines",
			"● 原因是局部软换行破坏了 2D TUI 边框。",
			"──────────────────────────────────────────────────────────",
			"  ▸▸ bypass permissions on (shift+tab to cycle)",
		}, "\n")
		items := ParseVTTranscript(raw)
		if len(items) < 2 {
			t.Fatalf("expected user + assistant items from VT fallback, got %+v", items)
		}
		if items[0].Role != RoleUser || !strings.Contains(items[0].Text, "报错原因") {
			t.Fatalf("expected first VT item to be user prompt, got %+v", items[0])
		}
		foundReply := false
		for _, it := range items {
			if strings.Contains(it.Text, "bypass permissions on") {
				t.Fatalf("expected TUI status footer stripped, got %+v", it)
			}
			if it.Role == RoleAssistant && strings.Contains(it.Text, "2D TUI 边框") {
				foundReply = true
			}
		}
		if !foundReply {
			t.Fatalf("expected assistant reply in VT fallback, got %+v", items)
		}
	})
}

func TestSplitMarkdownBlocksAndTruncation(t *testing.T) {
	md := "第一段说明。\n\n- 列表项一\n- 列表项二\n\n```python\nprint('hi')\n```"
	blocks := SplitMarkdownBlocks(md)
	if len(blocks) != 3 || blocks[0].Kind != BlockParagraph || blocks[1].Kind != BlockList || blocks[2].Kind != BlockCode || blocks[2].Lang != "python" {
		t.Fatalf("unexpected markdown blocks: %+v", blocks)
	}

	// Verify 20+20 line truncation on huge tool outputs.
	var bigLines []string
	for i := 1; i <= 100; i++ {
		bigLines = append(bigLines, fmt.Sprintf("line-%03d", i))
	}
	lines := []string{
		`{"type":"assistant","uuid":"a1","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"seq 100"}}]}}`,
		fmt.Sprintf(`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":%q}]}}`, strings.Join(bigLines, "\n")),
	}
	items := ParseTranscriptLines(FormatClaude, lines)
	if len(items) != 1 || items[0].Role != RoleToolGroup {
		t.Fatalf("expected 1 tool group, got %+v", items)
	}
	out := items[0].Tools[0].Output
	if !strings.Contains(out, "line-001") || !strings.Contains(out, "line-100") || strings.Contains(out, "line-050") {
		t.Fatalf("expected 20+20 truncation preserving head & tail, got:\n%s", out)
	}
}

func TestIsAgentTerminal(t *testing.T) {
	agentTerm := Terminal{Status: &Status{App: "claude-code"}}
	ompTerm := Terminal{Labels: map[string]string{"alma.session": "omp:id:123"}}
	shellTerm := Terminal{Foreground: &Foreground{Name: "zsh"}}
	if !IsAgentTerminal(agentTerm) || !IsAgentTerminal(ompTerm) {
		t.Fatal("expected claude-code and omp terminals recognized as Agent")
	}
	if IsAgentTerminal(shellTerm) {
		t.Fatal("expected plain zsh terminal NOT recognized as Agent")
	}
}
