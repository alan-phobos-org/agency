package stream

import (
	"fmt"
	"path/filepath"
	"strings"

	"phobos.org.uk/agency/internal/logging"
)

// DefaultToolEventLogger logs tool events with tool-specific formatting
type DefaultToolEventLogger struct {
	log *logging.TaskLogger
}

// NewToolEventLogger creates a new logger for tool events
func NewToolEventLogger(log *logging.TaskLogger) *DefaultToolEventLogger {
	return &DefaultToolEventLogger{log: log}
}

// Log logs a tool event with appropriate formatting
func (l *DefaultToolEventLogger) Log(event *ToolEvent) {
	if event == nil {
		return
	}
	switch event.Type {
	case EventSessionInit:
		l.log.Debug("session initialized", nil)
	case EventToolCall:
		l.logToolCall(event)
	case EventToolResult:
		l.logToolResult(event)
	case EventTextResponse:
		l.log.Debug("assistant response", map[string]any{
			"length": event.TextLength,
		})
	case EventComplete:
		l.logComplete(event)
	}
}

func (l *DefaultToolEventLogger) logToolCall(event *ToolEvent) {
	switch event.ToolName {
	case "Bash":
		cmd := truncate(getString(event.Input, "command"), 64)
		l.log.Info("bash command", map[string]any{"cmd": cmd})

	case "Read":
		fields := map[string]any{"path": getString(event.Input, "file_path")}
		if offset := getInt(event.Input, "offset"); offset > 0 {
			limit := getInt(event.Input, "limit")
			if limit > 0 {
				fields["lines"] = formatRange(offset, offset+limit)
			}
		}
		l.log.Info("read file", fields)

	case "Write":
		content := getString(event.Input, "content")
		l.log.Info("write file", map[string]any{
			"path":  getString(event.Input, "file_path"),
			"bytes": len(content),
		})

	case "Edit":
		l.log.Info("edit file", map[string]any{
			"path": filepath.Base(getString(event.Input, "file_path")),
			"old":  truncate(getString(event.Input, "old_string"), 24),
			"new":  truncate(getString(event.Input, "new_string"), 24),
		})

	case "Glob":
		fields := map[string]any{"pattern": getString(event.Input, "pattern")}
		if path := getString(event.Input, "path"); path != "" {
			fields["path"] = path
		}
		l.log.Info("glob search", fields)

	case "Grep":
		fields := map[string]any{"pattern": getString(event.Input, "pattern")}
		if t := getString(event.Input, "type"); t != "" {
			fields["type"] = t
		}
		if path := getString(event.Input, "path"); path != "" {
			fields["path"] = path
		}
		l.log.Info("grep search", fields)

	case "WebSearch":
		l.log.Info("web search", map[string]any{
			"query": getString(event.Input, "query"),
		})

	case "WebFetch":
		l.log.Info("web fetch", map[string]any{
			"url": truncate(getString(event.Input, "url"), 64),
		})

	case "Task":
		l.log.Info("spawn agent", map[string]any{
			"type": getString(event.Input, "subagent_type"),
			"desc": truncate(getString(event.Input, "description"), 32),
		})

	case "TodoWrite":
		todos := getArray(event.Input, "todos")
		pending, done := 0, 0
		for _, t := range todos {
			if m, ok := t.(map[string]any); ok {
				if m["status"] == "pending" {
					pending++
				}
				if m["status"] == "completed" {
					done++
				}
			}
		}
		l.log.Info("update todos", map[string]any{
			"count":   len(todos),
			"pending": pending,
			"done":    done,
		})

	case "AskUserQuestion":
		questions := getArray(event.Input, "questions")
		fields := map[string]any{"questions": len(questions)}
		if len(questions) > 0 {
			if q, ok := questions[0].(map[string]any); ok {
				if header := getString(q, "header"); header != "" {
					fields["header"] = header
				}
			}
		}
		l.log.Info("ask user", fields)

	default:
		fields := map[string]any{"tool": event.ToolName}
		if event.Input != nil {
			fields["input_keys"] = len(event.Input)
		}
		l.log.Info("tool call", fields)
	}
}

func (l *DefaultToolEventLogger) logToolResult(event *ToolEvent) {
	switch event.ToolName {
	case "Bash":
		exit := 0
		if event.IsError {
			exit = 1
		}
		l.log.Debug("bash result", map[string]any{
			"exit": exit,
			"out":  truncate(event.Output, 32),
		})

	case "Read":
		fields := map[string]any{"bytes": len(event.Output)}
		if event.IsError {
			fields["error"] = true
		}
		l.log.Debug("read result", fields)

	case "Write":
		l.log.Debug("write result", map[string]any{
			"ok": !event.IsError,
		})

	case "Edit":
		l.log.Debug("edit result", map[string]any{
			"ok": !event.IsError,
		})

	case "Glob":
		matches := countLines(event.Output)
		if event.IsError {
			matches = 0
		}
		l.log.Debug("glob result", map[string]any{"matches": matches})

	case "Grep":
		matches := countLines(event.Output)
		if event.IsError {
			matches = 0
		}
		l.log.Debug("grep result", map[string]any{"matches": matches})

	case "WebSearch":
		// Count URLs in output as proxy for result count
		results := strings.Count(event.Output, "](http")
		if event.IsError {
			results = 0
		}
		l.log.Debug("search result", map[string]any{"results": results})

	case "WebFetch":
		fields := map[string]any{"bytes": len(event.Output)}
		if event.IsError {
			fields["error"] = true
		}
		l.log.Debug("fetch result", fields)

	case "Task":
		l.log.Debug("agent result", map[string]any{
			"ok":    !event.IsError,
			"chars": len(event.Output),
		})

	default:
		fields := map[string]any{
			"tool":         event.ToolName,
			"output_bytes": len(event.Output),
		}
		if event.IsError {
			fields["error"] = true
		}
		l.log.Debug("tool result", fields)
	}
}

func (l *DefaultToolEventLogger) logComplete(event *ToolEvent) {
	if event.Metrics == nil {
		l.log.Info("execution complete", nil)
		return
	}

	l.log.Info("execution complete", map[string]any{
		"duration_ms": event.Metrics.DurationMS,
		"turns":       event.Metrics.NumTurns,
		"cost_usd":    event.Metrics.TotalCostUSD,
	})
}

// Helper functions

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func getString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getInt(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	if v, ok := m[key].(int); ok {
		return v
	}
	return 0
}

func getArray(m map[string]any, key string) []any {
	if m == nil {
		return nil
	}
	if v, ok := m[key].([]any); ok {
		return v
	}
	return nil
}

func formatRange(start, end int) string {
	if end <= start {
		return ""
	}
	return fmt.Sprintf("%d-%d", start, end)
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n")
}
