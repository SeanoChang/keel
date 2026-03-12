package loop

import (
	"encoding/json"
	"fmt"
	"strings"
)

// StreamEventKind categorizes events from Claude's --output-format stream-json.
type StreamEventKind string

const (
	EventToolUse    StreamEventKind = "tool_use"
	EventToolResult StreamEventKind = "tool_result"
	EventThinking   StreamEventKind = "thinking"
	EventText       StreamEventKind = "text"
	EventResult     StreamEventKind = "result"
)

// StreamEvent is a parsed event from the stream-json output.
type StreamEvent struct {
	Kind       StreamEventKind
	ToolName   string  // tool_use/tool_result: name of the tool
	ToolInput  string  // tool_use: compact input summary
	Text       string  // text/result: content
	IsError    bool    // result/tool_result: whether it errored
	Cost       float64 // result: total_cost_usd
	DurationMs int64   // result: duration_ms
	NumTurns   int     // result: num_turns
}

// --- JSON envelope types for stream-json parsing ---

type streamLine struct {
	Type       string         `json:"type"`
	Subtype    string         `json:"subtype"`
	Message    *streamMessage `json:"message"`
	Result     string         `json:"result"`
	IsError    bool           `json:"is_error"`
	Cost       float64        `json:"total_cost_usd"`
	DurationMs int64          `json:"duration_ms"`
	NumTurns   int            `json:"num_turns"`
}

type streamMessage struct {
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	Text      string          `json:"text"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// ParseStreamJSON parses a single JSON line from --output-format stream-json
// and returns any meaningful events found.
func ParseStreamJSON(line string) []StreamEvent {
	var envelope streamLine
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		return nil
	}

	switch envelope.Type {
	case "result":
		return []StreamEvent{{
			Kind:       EventResult,
			Text:       envelope.Result,
			IsError:    envelope.IsError,
			Cost:       envelope.Cost,
			DurationMs: envelope.DurationMs,
			NumTurns:   envelope.NumTurns,
		}}

	case "assistant":
		if envelope.Message == nil {
			return nil
		}
		var blocks []contentBlock
		if err := json.Unmarshal(envelope.Message.Content, &blocks); err != nil {
			return nil
		}
		var events []StreamEvent
		for _, block := range blocks {
			switch block.Type {
			case "tool_use":
				events = append(events, StreamEvent{
					Kind:      EventToolUse,
					ToolName:  block.Name,
					ToolInput: summarizeInput(block.Name, block.Input),
				})
			case "thinking":
				events = append(events, StreamEvent{Kind: EventThinking})
			case "text":
				if block.Text != "" {
					events = append(events, StreamEvent{
						Kind: EventText,
						Text: block.Text,
					})
				}
			}
		}
		return events

	case "user":
		// Tool results come back as user messages
		if envelope.Message == nil {
			return nil
		}
		var blocks []contentBlock
		if err := json.Unmarshal(envelope.Message.Content, &blocks); err != nil {
			return nil
		}
		var events []StreamEvent
		for _, block := range blocks {
			if block.Type == "tool_result" {
				events = append(events, StreamEvent{
					Kind:    EventToolResult,
					IsError: block.IsError,
				})
			}
		}
		return events
	}

	return nil
}

// summarizeInput creates a short human-readable description of a tool's input.
func summarizeInput(tool string, raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}

	// Known tools with specific field preferences
	switch tool {
	case "Read", "Write", "Edit":
		return extractString(m, "file_path", shortPath)
	case "Bash":
		return extractString(m, "command", truncate60)
	case "Grep":
		s := extractString(m, "pattern", identity)
		if s != "" {
			return fmt.Sprintf("/%s/", s)
		}
	case "Glob":
		return extractString(m, "pattern", identity)
	case "WebSearch":
		return extractString(m, "query", identity)
	case "WebFetch":
		return extractString(m, "url", identity)
	case "Agent":
		return extractString(m, "description", identity)
	case "Skill":
		return extractString(m, "skill", identity)
	case "TodoWrite":
		return extractString(m, "todos", truncate60)
	}

	// Generic fallback: grab the first short string field from the input
	return firstStringField(m)
}

// firstStringField returns the first short string value from a JSON object.
// Used as a generic fallback for unknown/MCP tools.
func firstStringField(m map[string]json.RawMessage) string {
	for _, v := range m {
		var s string
		if err := json.Unmarshal(v, &s); err == nil && s != "" {
			if len(s) > 80 {
				s = s[:80] + "..."
			}
			return s
		}
	}
	return ""
}

// ShortToolName returns a display-friendly tool name.
// MCP tools like "mcp__claude_ai_Notion__notion-search" become "Notion/notion-search".
func ShortToolName(name string) string {
	if !strings.HasPrefix(name, "mcp__") {
		return name
	}
	// mcp__<server>__<tool> → server/tool
	rest := strings.TrimPrefix(name, "mcp__")
	parts := strings.SplitN(rest, "__", 2)
	if len(parts) == 2 {
		server := parts[0]
		// Trim common prefixes like "claude_ai_"
		server = strings.TrimPrefix(server, "claude_ai_")
		return server + "/" + parts[1]
	}
	return name
}

func extractString(m map[string]json.RawMessage, key string, transform func(string) string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return ""
	}
	return transform(s)
}

func identity(s string) string { return s }

func truncate60(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

// shortPath returns the last 2 path components for compact display.
func shortPath(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) > 2 {
		return "…/" + strings.Join(parts[len(parts)-2:], "/")
	}
	return p
}
