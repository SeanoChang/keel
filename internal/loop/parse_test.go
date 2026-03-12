package loop

import (
	"testing"
)

func TestParseStreamJSON_ToolUse(t *testing.T) {
	line := `{"type":"assistant","message":{"model":"claude-opus-4-6","id":"msg_01","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"WebSearch","input":{"query":"Iran USA news March 2026"}}],"stop_reason":null},"session_id":"abc"}`

	events := ParseStreamJSON(line)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Kind != EventToolUse {
		t.Errorf("expected EventToolUse, got %s", ev.Kind)
	}
	if ev.ToolName != "WebSearch" {
		t.Errorf("expected WebSearch, got %s", ev.ToolName)
	}
	if ev.ToolInput != "Iran USA news March 2026" {
		t.Errorf("expected query input, got %q", ev.ToolInput)
	}
}

func TestParseStreamJSON_ReadTool(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01","name":"Read","input":{"file_path":"/Users/sean/dev/projects/keel/internal/loop/loop.go"}}]}}`

	events := ParseStreamJSON(line)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ToolInput != "…/loop/loop.go" {
		t.Errorf("expected short path, got %q", events[0].ToolInput)
	}
}

func TestParseStreamJSON_TextOutput(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"Here is the answer."}]}}`

	events := ParseStreamJSON(line)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Kind != EventText {
		t.Errorf("expected EventText, got %s", events[0].Kind)
	}
	if events[0].Text != "Here is the answer." {
		t.Errorf("unexpected text: %q", events[0].Text)
	}
}

func TestParseStreamJSON_Result(t *testing.T) {
	line := `{"type":"result","subtype":"success","is_error":false,"duration_ms":19431,"num_turns":4,"result":"The final response text.","total_cost_usd":0.16370575}`

	events := ParseStreamJSON(line)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Kind != EventResult {
		t.Errorf("expected EventResult, got %s", ev.Kind)
	}
	if ev.Text != "The final response text." {
		t.Errorf("unexpected result text: %q", ev.Text)
	}
	if ev.Cost < 0.16 || ev.Cost > 0.17 {
		t.Errorf("unexpected cost: %f", ev.Cost)
	}
	if ev.DurationMs != 19431 {
		t.Errorf("unexpected duration: %d", ev.DurationMs)
	}
	if ev.NumTurns != 4 {
		t.Errorf("unexpected num_turns: %d", ev.NumTurns)
	}
	if ev.IsError {
		t.Error("expected IsError=false")
	}
}

func TestParseStreamJSON_SystemIgnored(t *testing.T) {
	line := `{"type":"system","subtype":"init","cwd":"/Users/sean","session_id":"abc"}`

	events := ParseStreamJSON(line)
	if len(events) != 0 {
		t.Errorf("expected 0 events for system line, got %d", len(events))
	}
}

func TestParseStreamJSON_InvalidJSON(t *testing.T) {
	events := ParseStreamJSON("not json at all")
	if len(events) != 0 {
		t.Errorf("expected 0 events for invalid JSON, got %d", len(events))
	}
}

func TestParseStreamJSON_Thinking(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Let me think about this..."}]}}`

	events := ParseStreamJSON(line)
	if len(events) != 1 {
		t.Fatalf("expected 1 event for thinking block, got %d", len(events))
	}
	if events[0].Kind != EventThinking {
		t.Errorf("expected EventThinking, got %s", events[0].Kind)
	}
}

func TestParseStreamJSON_ToolResult(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":[{"type":"tool_reference","tool_name":"WebSearch"}]}]}}`

	events := ParseStreamJSON(line)
	if len(events) != 1 {
		t.Fatalf("expected 1 event for tool_result, got %d", len(events))
	}
	if events[0].Kind != EventToolResult {
		t.Errorf("expected EventToolResult, got %s", events[0].Kind)
	}
}

func TestParseStreamJSON_MCPTool(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01","name":"mcp__claude_ai_Notion__notion-search","input":{"query":"meeting notes"}}]}}`

	events := ParseStreamJSON(line)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ToolName != "mcp__claude_ai_Notion__notion-search" {
		t.Errorf("expected full MCP tool name, got %s", events[0].ToolName)
	}
	if events[0].ToolInput != "meeting notes" {
		t.Errorf("expected generic input extraction, got %q", events[0].ToolInput)
	}
}

func TestShortToolName(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Read", "Read"},
		{"WebSearch", "WebSearch"},
		{"mcp__claude_ai_Notion__notion-search", "Notion/notion-search"},
		{"mcp__claude_ai_Gmail__gmail-send", "Gmail/gmail-send"},
		{"TodoWrite", "TodoWrite"},
	}
	for _, tt := range tests {
		got := ShortToolName(tt.input)
		if got != tt.want {
			t.Errorf("ShortToolName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseStreamJSON_BashTool(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"go build ./..."}}]}}`

	events := ParseStreamJSON(line)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ToolInput != "go build ./..." {
		t.Errorf("unexpected input: %q", events[0].ToolInput)
	}
}

func TestParseStreamJSON_GrepTool(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01","name":"Grep","input":{"pattern":"ParseTool.*"}}]}}`

	events := ParseStreamJSON(line)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ToolInput != "/ParseTool.*/" {
		t.Errorf("unexpected input: %q", events[0].ToolInput)
	}
}

func TestShortPath(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"/Users/sean/dev/projects/keel/internal/loop/loop.go", "…/loop/loop.go"},
		{"loop.go", "loop.go"},
		{"a/b", "a/b"},
		{"/a/b/c", "…/b/c"},
	}
	for _, tt := range tests {
		got := shortPath(tt.input)
		if got != tt.want {
			t.Errorf("shortPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
