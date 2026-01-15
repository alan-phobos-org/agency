// Package history provides task history storage with outline extraction.
package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Store manages task history persistence.
type Store struct {
	dir string // Base directory for history files

	mu      sync.RWMutex
	entries map[string]*Entry // In-memory cache keyed by task ID
}

// Entry represents a completed task in history.
type Entry struct {
	TaskID          string      `json:"task_id"`
	SessionID       string      `json:"session_id"`
	State           string      `json:"state"`
	Prompt          string      `json:"prompt"`
	PromptPreview   string      `json:"prompt_preview"` // First 200 chars
	Model           string      `json:"model"`
	StartedAt       time.Time   `json:"started_at"`
	CompletedAt     time.Time   `json:"completed_at"`
	DurationSeconds float64     `json:"duration_seconds"`
	ExitCode        *int        `json:"exit_code,omitempty"`
	Output          string      `json:"output,omitempty"`
	OutputPreview   string      `json:"output_preview,omitempty"` // First 200 chars
	Error           *EntryError `json:"error,omitempty"`
	TokenUsage      *TokenUsage `json:"token_usage,omitempty"`
	Steps           []Step      `json:"steps,omitempty"` // Outline of execution steps
	HasDebugLog     bool        `json:"has_debug_log"`   // Whether full debug log exists
}

// EntryError captures error details.
type EntryError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// TokenUsage captures token consumption.
type TokenUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// Step represents a single step in the task execution outline.
type Step struct {
	Type          string `json:"type"`                     // "tool_call", "text", "error"
	Tool          string `json:"tool,omitempty"`           // Tool name for tool_call
	InputPreview  string `json:"input_preview,omitempty"`  // First 200 chars of input
	OutputPreview string `json:"output_preview,omitempty"` // First 200 chars of output
	Truncated     bool   `json:"truncated,omitempty"`      // Whether content was truncated
}

// ListOptions controls pagination for List.
type ListOptions struct {
	Page  int // 1-indexed page number
	Limit int // Items per page (max 100)
}

// ListResult contains paginated history entries.
type ListResult struct {
	Entries    []EntrySummary `json:"entries"`
	Page       int            `json:"page"`
	Limit      int            `json:"limit"`
	Total      int            `json:"total"`
	TotalPages int            `json:"total_pages"`
}

// EntrySummary is a lightweight version of Entry for list responses.
type EntrySummary struct {
	TaskID          string      `json:"task_id"`
	SessionID       string      `json:"session_id"`
	State           string      `json:"state"`
	PromptPreview   string      `json:"prompt_preview"`
	Model           string      `json:"model"`
	StartedAt       time.Time   `json:"started_at"`
	CompletedAt     time.Time   `json:"completed_at"`
	DurationSeconds float64     `json:"duration_seconds"`
	ExitCode        *int        `json:"exit_code,omitempty"`
	Error           *EntryError `json:"error,omitempty"`
	HasDebugLog     bool        `json:"has_debug_log"`
}

// Retention limits
const (
	MaxOutlineEntries = 100
	MaxDebugEntries   = 20
	PreviewLength     = 200
)

// NewStore creates a new history store at the given directory.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating history directory: %w", err)
	}

	s := &Store{
		dir:     dir,
		entries: make(map[string]*Entry),
	}

	// Load existing entries from disk
	if err := s.load(); err != nil {
		return nil, fmt.Errorf("loading history: %w", err)
	}

	return s, nil
}

// Save persists a task entry to history.
// It also triggers pruning if limits are exceeded.
func (s *Store) Save(entry *Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate previews
	entry.PromptPreview = truncate(entry.Prompt, PreviewLength)
	entry.OutputPreview = truncate(entry.Output, PreviewLength)

	// Save outline file
	outlinePath := s.outlinePath(entry.TaskID)
	if err := writeJSON(outlinePath, entry); err != nil {
		return fmt.Errorf("saving outline: %w", err)
	}

	s.entries[entry.TaskID] = entry

	// Prune old entries
	s.pruneUnlocked()

	return nil
}

// SaveDebugLog saves the full debug log for a task.
func (s *Store) SaveDebugLog(taskID string, debugLog []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	debugPath := s.debugPath(taskID)
	if err := os.WriteFile(debugPath, debugLog, 0644); err != nil {
		return fmt.Errorf("saving debug log: %w", err)
	}

	// Update entry to indicate debug log exists
	if entry, ok := s.entries[taskID]; ok {
		entry.HasDebugLog = true
		if err := writeJSON(s.outlinePath(taskID), entry); err != nil {
			return fmt.Errorf("updating outline: %w", err)
		}
	}

	return nil
}

// Get retrieves a task entry by ID.
func (s *Store) Get(taskID string) (*Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.entries[taskID]
	if !ok {
		return nil, fmt.Errorf("%s not found in history", taskID)
	}
	return entry, nil
}

// GetDebugLog retrieves the full debug log for a task.
func (s *Store) GetDebugLog(taskID string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	debugPath := s.debugPath(taskID)
	data, err := os.ReadFile(debugPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("debug log for %s not found", taskID)
		}
		return nil, fmt.Errorf("reading debug log: %w", err)
	}
	return data, nil
}

// List returns paginated history entries, newest first.
func (s *Store) List(opts ListOptions) ListResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Apply defaults
	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.Limit < 1 {
		opts.Limit = 20
	}
	if opts.Limit > 100 {
		opts.Limit = 100
	}

	// Collect and sort entries by completion time (newest first)
	sorted := make([]*Entry, 0, len(s.entries))
	for _, e := range s.entries {
		sorted = append(sorted, e)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CompletedAt.After(sorted[j].CompletedAt)
	})

	total := len(sorted)
	totalPages := (total + opts.Limit - 1) / opts.Limit

	// Calculate slice bounds
	start := (opts.Page - 1) * opts.Limit
	end := start + opts.Limit
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	// Convert to summaries
	entries := make([]EntrySummary, 0, end-start)
	for _, e := range sorted[start:end] {
		entries = append(entries, EntrySummary{
			TaskID:          e.TaskID,
			SessionID:       e.SessionID,
			State:           e.State,
			PromptPreview:   e.PromptPreview,
			Model:           e.Model,
			StartedAt:       e.StartedAt,
			CompletedAt:     e.CompletedAt,
			DurationSeconds: e.DurationSeconds,
			ExitCode:        e.ExitCode,
			Error:           e.Error,
			HasDebugLog:     e.HasDebugLog,
		})
	}

	return ListResult{
		Entries:    entries,
		Page:       opts.Page,
		Limit:      opts.Limit,
		Total:      total,
		TotalPages: totalPages,
	}
}

// load reads all existing entries from disk.
func (s *Store) load() error {
	pattern := filepath.Join(s.dir, "*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue // Skip unreadable files
		}

		var entry Entry
		if err := json.Unmarshal(data, &entry); err != nil {
			continue // Skip invalid JSON
		}

		// Check if debug log exists
		debugPath := s.debugPath(entry.TaskID)
		if _, err := os.Stat(debugPath); err == nil {
			entry.HasDebugLog = true
		}

		s.entries[entry.TaskID] = &entry
	}

	return nil
}

// pruneUnlocked removes old entries exceeding retention limits.
// Must be called with lock held.
func (s *Store) pruneUnlocked() {
	// Sort by completion time (newest first)
	sorted := make([]*Entry, 0, len(s.entries))
	for _, e := range s.entries {
		sorted = append(sorted, e)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CompletedAt.After(sorted[j].CompletedAt)
	})

	// Delete oldest entries exceeding outline limit
	if len(sorted) > MaxOutlineEntries {
		for i := MaxOutlineEntries; i < len(sorted); i++ {
			taskID := sorted[i].TaskID
			os.Remove(s.outlinePath(taskID))
			os.Remove(s.debugPath(taskID)) // Also remove debug if exists
			delete(s.entries, taskID)
		}
		sorted = sorted[:MaxOutlineEntries]
	}

	// Prune debug logs for older entries (keep only newest MaxDebugEntries)
	for i := MaxDebugEntries; i < len(sorted); i++ {
		taskID := sorted[i].TaskID
		debugPath := s.debugPath(taskID)
		if _, err := os.Stat(debugPath); err == nil {
			os.Remove(debugPath)
			if entry, ok := s.entries[taskID]; ok {
				entry.HasDebugLog = false
				// Update the file to reflect HasDebugLog = false
				writeJSON(s.outlinePath(taskID), entry)
			}
		}
	}
}

func (s *Store) outlinePath(taskID string) string {
	return filepath.Join(s.dir, taskID+".json")
}

func (s *Store) debugPath(taskID string) string {
	return filepath.Join(s.dir, taskID+".debug.log")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
