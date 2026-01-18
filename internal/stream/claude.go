package stream

import (
	"encoding/json"
	"strings"
	"time"
)

// ClaudeStreamEvent represents the raw JSON format from Claude CLI --output-format stream-json
type ClaudeStreamEvent struct {
	Type      string `json:"type"`    // system, assistant, user, result
	Subtype   string `json:"subtype"` // init, success, error
	SessionID string `json:"session_id,omitempty"`
	Model     string `json:"model,omitempty"`
	Tools     []any  `json:"tools,omitempty"`
	Message   struct {
		Content []ContentBlock `json:"content"`
	} `json:"message,omitempty"`
	DurationMS   int     `json:"duration_ms,omitempty"`
	NumTurns     int     `json:"num_turns,omitempty"`
	TotalCostUSD float64 `json:"total_cost_usd,omitempty"`
}

// ContentBlock represents a content block in a Claude message
type ContentBlock struct {
	Type      string          `json:"type"`         // text, tool_use, tool_result
	ID        string          `json:"id,omitempty"` // tool_use ID for correlation
	Name      string          `json:"name,omitempty"`
	Text      string          `json:"text,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"` // for tool_result
	Content   json.RawMessage `json:"content,omitempty"`     // tool_result content
	IsError   bool            `json:"is_error,omitempty"`
}

// ClaudeStreamParser parses Claude CLI stream-json format
type ClaudeStreamParser struct {
	pendingCalls map[string]ContentBlock // ID -> tool_use block for correlation
}

// NewClaudeStreamParser creates a new parser for Claude stream output
func NewClaudeStreamParser() *ClaudeStreamParser {
	return &ClaudeStreamParser{
		pendingCalls: make(map[string]ContentBlock),
	}
}

// Provider returns the provider name
func (p *ClaudeStreamParser) Provider() string {
	return "claude"
}

// ParseLine parses a single JSON line from Claude's stream output.
// Returns a slice of events since one line may contain multiple tool calls.
func (p *ClaudeStreamParser) ParseLine(line []byte) ([]*ToolEvent, error) {
	// Skip empty lines
	if len(line) == 0 {
		return nil, nil
	}

	var raw ClaudeStreamEvent
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}

	now := time.Now()
	var events []*ToolEvent

	switch raw.Type {
	case "system":
		if raw.Subtype == "init" {
			events = append(events, &ToolEvent{
				Type:      EventSessionInit,
				Timestamp: now,
			})
		}

	case "assistant":
		for _, block := range raw.Message.Content {
			switch block.Type {
			case "tool_use":
				// Track for later correlation with result
				p.pendingCalls[block.ID] = block

				// Parse input (ignore unmarshal errors - input will be nil if malformed)
				var input map[string]any
				if len(block.Input) > 0 {
					if err := json.Unmarshal(block.Input, &input); err != nil {
						// Log as raw string if JSON parse fails
						input = map[string]any{"_raw": string(block.Input)}
					}
				}

				events = append(events, &ToolEvent{
					Type:      EventToolCall,
					Timestamp: now,
					ToolName:  block.Name,
					ToolID:    block.ID,
					Input:     input,
				})

			case "text":
				if len(block.Text) > 0 {
					events = append(events, &ToolEvent{
						Type:       EventTextResponse,
						Timestamp:  now,
						TextLength: len(block.Text),
					})
				}
			}
		}

	case "user":
		for _, block := range raw.Message.Content {
			if block.Type == "tool_result" {
				// Find the original tool_use for context
				toolUse, ok := p.pendingCalls[block.ToolUseID]
				if ok {
					delete(p.pendingCalls, block.ToolUseID)

					events = append(events, &ToolEvent{
						Type:      EventToolResult,
						Timestamp: now,
						ToolName:  toolUse.Name,
						ToolID:    block.ToolUseID,
						Output:    extractContent(block.Content),
						IsError:   block.IsError,
					})
				}
			}
		}

	case "result":
		events = append(events, &ToolEvent{
			Type:      EventComplete,
			Timestamp: now,
			Metrics: &CompletionMetrics{
				DurationMS:   raw.DurationMS,
				NumTurns:     raw.NumTurns,
				TotalCostUSD: raw.TotalCostUSD,
			},
		})
	}

	return events, nil
}

// extractContent normalizes content which can be string or array of blocks
func extractContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try as string first
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}

	// Try as array of content blocks
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var sb strings.Builder
		for _, b := range blocks {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		return sb.String()
	}

	return ""
}
