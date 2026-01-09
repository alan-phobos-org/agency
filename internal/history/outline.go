package history

import (
	"encoding/json"
	"strings"
)

// ExtractSteps parses Claude's JSON output and extracts an outline of execution steps.
// If the output is not valid JSON, returns a single text step with the raw output.
func ExtractSteps(output []byte) []Step {
	// Try to parse as Claude's streaming JSON format
	// Claude outputs conversation messages with tool calls and results
	var messages []claudeMessage
	if err := json.Unmarshal(output, &messages); err != nil {
		// Try single message
		var msg claudeMessage
		if err := json.Unmarshal(output, &msg); err != nil {
			// Not valid JSON - return as single text step
			return []Step{{
				Type:          "text",
				OutputPreview: truncate(string(output), PreviewLength),
				Truncated:     len(output) > PreviewLength,
			}}
		}
		messages = []claudeMessage{msg}
	}

	// First pass: collect all tool calls by ID
	toolCalls := make(map[string]*Step)
	var steps []Step

	for _, msg := range messages {
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				if text := strings.TrimSpace(block.Text); text != "" {
					steps = append(steps, Step{
						Type:          "text",
						OutputPreview: truncate(text, PreviewLength),
						Truncated:     len(text) > PreviewLength,
					})
				}

			case "tool_use":
				inputStr := formatInput(block.Input)
				step := Step{
					Type:         "tool_call",
					Tool:         block.Name,
					InputPreview: truncate(inputStr, PreviewLength),
					Truncated:    len(inputStr) > PreviewLength,
				}
				steps = append(steps, step)
				toolCalls[block.ID] = &steps[len(steps)-1]

			case "tool_result":
				contentStr := formatContent(block.Content)
				if step, ok := toolCalls[block.ToolUseID]; ok {
					step.OutputPreview = truncate(contentStr, PreviewLength)
					if len(contentStr) > PreviewLength {
						step.Truncated = true
					}
				}
			}
		}
	}

	// If no steps extracted, return raw output as text
	if len(steps) == 0 {
		return []Step{{
			Type:          "text",
			OutputPreview: truncate(string(output), PreviewLength),
			Truncated:     len(output) > PreviewLength,
		}}
	}

	return steps
}

// claudeMessage represents a message in Claude's conversation output.
type claudeMessage struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

// contentBlock represents a content block in a Claude message.
type contentBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`

	// Tool result fields
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"` // Can be string or array
}

func formatInput(input any) string {
	if input == nil {
		return ""
	}

	switch v := input.(type) {
	case string:
		return v
	case map[string]any:
		// Format as key: value pairs, one per line
		var parts []string
		for key, val := range v {
			valStr := formatValue(val)
			parts = append(parts, key+": "+valStr)
		}
		return strings.Join(parts, "\n")
	default:
		data, _ := json.Marshal(v)
		return string(data)
	}
}

func formatContent(content any) string {
	if content == nil {
		return ""
	}

	switch v := content.(type) {
	case string:
		return v
	case []any:
		// Array of content blocks
		var parts []string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if text, ok := m["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		data, _ := json.Marshal(v)
		return string(data)
	}
}

func formatValue(v any) string {
	switch val := v.(type) {
	case string:
		// For multi-line strings, show first few lines
		lines := strings.Split(val, "\n")
		if len(lines) > 3 {
			return strings.Join(lines[:3], "\n") + "\n..."
		}
		return val
	case bool, int, int64, float64:
		return jsonString(val)
	default:
		return jsonString(val)
	}
}

func jsonString(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
