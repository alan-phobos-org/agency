// Package logging provides structured JSON logging with levels and queryable storage.
package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Level represents log severity levels
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// levelPriority returns numeric priority for level comparison
func levelPriority(l Level) int {
	switch l {
	case LevelDebug:
		return 0
	case LevelInfo:
		return 1
	case LevelWarn:
		return 2
	case LevelError:
		return 3
	default:
		return 1
	}
}

// Entry represents a single log entry
type Entry struct {
	Timestamp time.Time      `json:"timestamp"`
	Level     Level          `json:"level"`
	Message   string         `json:"message"`
	Component string         `json:"component,omitempty"`
	TaskID    string         `json:"task_id,omitempty"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// Logger provides structured logging with in-memory storage for querying
type Logger struct {
	mu         sync.RWMutex
	output     io.Writer
	level      Level
	component  string
	entries    []Entry
	maxEntries int
	counts     map[Level]int64
}

// Config holds logger configuration
type Config struct {
	Output     io.Writer // Output writer (default: os.Stderr)
	Level      Level     // Minimum log level (default: info)
	Component  string    // Component name for all entries
	MaxEntries int       // Max entries to keep in memory (default: 1000)
}

// New creates a new logger with the given configuration
func New(cfg Config) *Logger {
	if cfg.Output == nil {
		cfg.Output = os.Stderr
	}
	if cfg.Level == "" {
		cfg.Level = LevelInfo
	}
	if cfg.MaxEntries == 0 {
		cfg.MaxEntries = 1000
	}
	return &Logger{
		output:     cfg.Output,
		level:      cfg.Level,
		component:  cfg.Component,
		entries:    make([]Entry, 0, cfg.MaxEntries),
		maxEntries: cfg.MaxEntries,
		counts:     make(map[Level]int64),
	}
}

// SetLevel changes the minimum log level
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// log writes a log entry if it meets the level threshold
func (l *Logger) log(level Level, msg string, fields map[string]any) {
	if levelPriority(level) < levelPriority(l.level) {
		return
	}

	entry := Entry{
		Timestamp: time.Now().UTC(),
		Level:     level,
		Message:   msg,
		Component: l.component,
		Fields:    fields,
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Update counts
	l.counts[level]++

	// Store entry (ring buffer)
	if len(l.entries) >= l.maxEntries {
		// Shift entries left, dropping oldest
		copy(l.entries, l.entries[1:])
		l.entries = l.entries[:len(l.entries)-1]
	}
	l.entries = append(l.entries, entry)

	// Write to output as JSON
	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(l.output, `{"level":"error","message":"failed to marshal log entry: %s"}`+"\n", err)
		return
	}
	l.output.Write(append(data, '\n'))
}

// Debug logs at debug level
func (l *Logger) Debug(msg string, fields ...map[string]any) {
	var f map[string]any
	if len(fields) > 0 {
		f = fields[0]
	}
	l.log(LevelDebug, msg, f)
}

// Info logs at info level
func (l *Logger) Info(msg string, fields ...map[string]any) {
	var f map[string]any
	if len(fields) > 0 {
		f = fields[0]
	}
	l.log(LevelInfo, msg, f)
}

// Warn logs at warn level
func (l *Logger) Warn(msg string, fields ...map[string]any) {
	var f map[string]any
	if len(fields) > 0 {
		f = fields[0]
	}
	l.log(LevelWarn, msg, f)
}

// Error logs at error level
func (l *Logger) Error(msg string, fields ...map[string]any) {
	var f map[string]any
	if len(fields) > 0 {
		f = fields[0]
	}
	l.log(LevelError, msg, f)
}

// WithTask returns a task-scoped logger that adds task_id to all entries
func (l *Logger) WithTask(taskID string) *TaskLogger {
	return &TaskLogger{parent: l, taskID: taskID}
}

// TaskLogger is a logger scoped to a specific task
type TaskLogger struct {
	parent *Logger
	taskID string
}

func (t *TaskLogger) log(level Level, msg string, fields map[string]any) {
	if levelPriority(level) < levelPriority(t.parent.level) {
		return
	}

	entry := Entry{
		Timestamp: time.Now().UTC(),
		Level:     level,
		Message:   msg,
		Component: t.parent.component,
		TaskID:    t.taskID,
		Fields:    fields,
	}

	t.parent.mu.Lock()
	defer t.parent.mu.Unlock()

	t.parent.counts[level]++

	if len(t.parent.entries) >= t.parent.maxEntries {
		copy(t.parent.entries, t.parent.entries[1:])
		t.parent.entries = t.parent.entries[:len(t.parent.entries)-1]
	}
	t.parent.entries = append(t.parent.entries, entry)

	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(t.parent.output, `{"level":"error","message":"failed to marshal log entry: %s"}`+"\n", err)
		return
	}
	t.parent.output.Write(append(data, '\n'))
}

func (t *TaskLogger) Debug(msg string, fields ...map[string]any) {
	var f map[string]any
	if len(fields) > 0 {
		f = fields[0]
	}
	t.log(LevelDebug, msg, f)
}

func (t *TaskLogger) Info(msg string, fields ...map[string]any) {
	var f map[string]any
	if len(fields) > 0 {
		f = fields[0]
	}
	t.log(LevelInfo, msg, f)
}

func (t *TaskLogger) Warn(msg string, fields ...map[string]any) {
	var f map[string]any
	if len(fields) > 0 {
		f = fields[0]
	}
	t.log(LevelWarn, msg, f)
}

func (t *TaskLogger) Error(msg string, fields ...map[string]any) {
	var f map[string]any
	if len(fields) > 0 {
		f = fields[0]
	}
	t.log(LevelError, msg, f)
}

// Query parameters for filtering logs
type Query struct {
	Level     Level     // Filter by minimum level
	TaskID    string    // Filter by task ID
	Since     time.Time // Filter entries after this time
	Until     time.Time // Filter entries before this time
	Limit     int       // Max entries to return (0 = all)
	Component string    // Filter by component
}

// QueryResult contains filtered log entries and metadata
type QueryResult struct {
	Entries []Entry `json:"entries"`
	Total   int     `json:"total"`  // Total entries matching filter (before limit)
	Counts  Stats   `json:"counts"` // Overall counts by level
}

// Stats contains log statistics
type Stats struct {
	Debug int64 `json:"debug"`
	Info  int64 `json:"info"`
	Warn  int64 `json:"warn"`
	Error int64 `json:"error"`
	Total int64 `json:"total"`
}

// Query returns log entries matching the filter criteria
func (l *Logger) Query(q Query) QueryResult {
	l.mu.RLock()
	defer l.mu.RUnlock()

	// Get current counts
	stats := Stats{
		Debug: l.counts[LevelDebug],
		Info:  l.counts[LevelInfo],
		Warn:  l.counts[LevelWarn],
		Error: l.counts[LevelError],
	}
	stats.Total = stats.Debug + stats.Info + stats.Warn + stats.Error

	var filtered []Entry
	for _, e := range l.entries {
		// Level filter
		if q.Level != "" && levelPriority(e.Level) < levelPriority(q.Level) {
			continue
		}
		// Task filter
		if q.TaskID != "" && e.TaskID != q.TaskID {
			continue
		}
		// Time filters
		if !q.Since.IsZero() && e.Timestamp.Before(q.Since) {
			continue
		}
		if !q.Until.IsZero() && e.Timestamp.After(q.Until) {
			continue
		}
		// Component filter
		if q.Component != "" && e.Component != q.Component {
			continue
		}
		filtered = append(filtered, e)
	}

	total := len(filtered)

	// Apply limit
	if q.Limit > 0 && len(filtered) > q.Limit {
		// Return most recent entries
		filtered = filtered[len(filtered)-q.Limit:]
	}

	return QueryResult{
		Entries: filtered,
		Total:   total,
		Counts:  stats,
	}
}

// Stats returns current log statistics without entries
func (l *Logger) Stats() Stats {
	l.mu.RLock()
	defer l.mu.RUnlock()

	stats := Stats{
		Debug: l.counts[LevelDebug],
		Info:  l.counts[LevelInfo],
		Warn:  l.counts[LevelWarn],
		Error: l.counts[LevelError],
	}
	stats.Total = stats.Debug + stats.Info + stats.Warn + stats.Error
	return stats
}

// Clear removes all stored entries and resets counts
func (l *Logger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = make([]Entry, 0, l.maxEntries)
	l.counts = make(map[Level]int64)
}
