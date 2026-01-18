// Package stream provides streaming log parsing for AI agent output.
package stream

import (
	"time"
)

// ToolEventType represents the type of stream event
type ToolEventType int

const (
	// EventSessionInit indicates session initialization
	EventSessionInit ToolEventType = iota
	// EventToolCall indicates a tool is being invoked
	EventToolCall
	// EventToolResult indicates a tool has returned a result
	EventToolResult
	// EventTextResponse indicates an assistant text response
	EventTextResponse
	// EventComplete indicates the execution has completed
	EventComplete
)

// ToolEvent represents a normalized tool interaction event.
// This is the provider-agnostic event model that parsers emit.
type ToolEvent struct {
	Type      ToolEventType
	Timestamp time.Time

	// For ToolCall events
	ToolName string         // "Bash", "Read", etc.
	ToolID   string         // Correlation ID
	Input    map[string]any // Parsed tool input

	// For ToolResult events
	Output  string // Tool output (may be truncated for logging)
	IsError bool   // Whether tool failed

	// For TextResponse events
	TextLength int // Length of text response

	// For Complete events
	Metrics *CompletionMetrics
}

// CompletionMetrics contains execution metrics from the final result event
type CompletionMetrics struct {
	DurationMS   int
	NumTurns     int
	TotalCostUSD float64
	InputTokens  int
	OutputTokens int
}

// StreamParser converts provider-specific stream format to ToolEvents
type StreamParser interface {
	// ParseLine parses a single line from the stream.
	// Returns nil if line doesn't produce an event (e.g., partial data).
	ParseLine(line []byte) ([]*ToolEvent, error)

	// Provider returns the provider name for logging
	Provider() string
}

// ToolEventLogger logs tool events in human-readable format
type ToolEventLogger interface {
	Log(event *ToolEvent)
}
