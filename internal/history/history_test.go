package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStore_SaveAndGet(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	entry := &Entry{
		TaskID:          "task-123",
		SessionID:       "session-456",
		State:           "completed",
		Prompt:          "Fix the bug in auth.go",
		Model:           "sonnet",
		StartedAt:       time.Now().Add(-time.Minute),
		CompletedAt:     time.Now(),
		DurationSeconds: 60.0,
		Output:          "I've fixed the bug in auth.go",
	}

	err = store.Save(entry)
	require.NoError(t, err)

	// Check file was created
	_, err = os.Stat(filepath.Join(dir, "task-123.json"))
	require.NoError(t, err)

	// Retrieve and verify
	got, err := store.Get("task-123")
	require.NoError(t, err)
	require.Equal(t, entry.TaskID, got.TaskID)
	require.Equal(t, entry.SessionID, got.SessionID)
	require.Equal(t, entry.Prompt, got.Prompt)
	require.Equal(t, entry.Prompt, got.PromptPreview) // Under 200 chars
}

func TestStore_PreviewTruncation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	longPrompt := make([]byte, 300)
	for i := range longPrompt {
		longPrompt[i] = 'a'
	}

	entry := &Entry{
		TaskID:      "task-long",
		Prompt:      string(longPrompt),
		Output:      string(longPrompt),
		CompletedAt: time.Now(),
	}

	err = store.Save(entry)
	require.NoError(t, err)

	got, err := store.Get("task-long")
	require.NoError(t, err)

	// Preview should be truncated to 200 chars + "..."
	require.Len(t, got.PromptPreview, 203)
	require.True(t, len(got.PromptPreview) < len(got.Prompt))
}

func TestStore_DebugLog(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	entry := &Entry{
		TaskID:      "task-debug",
		CompletedAt: time.Now(),
	}

	err = store.Save(entry)
	require.NoError(t, err)

	debugData := []byte(`{"session_id": "test", "result": "done"}`)
	err = store.SaveDebugLog("task-debug", debugData)
	require.NoError(t, err)

	// Verify HasDebugLog is set
	got, err := store.Get("task-debug")
	require.NoError(t, err)
	require.True(t, got.HasDebugLog)

	// Retrieve debug log
	retrieved, err := store.GetDebugLog("task-debug")
	require.NoError(t, err)
	require.Equal(t, debugData, retrieved)
}

func TestStore_List(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	// Create 5 entries with different completion times
	for i := 0; i < 5; i++ {
		entry := &Entry{
			TaskID:      "task-" + string(rune('a'+i)),
			CompletedAt: time.Now().Add(time.Duration(i) * time.Minute),
		}
		err = store.Save(entry)
		require.NoError(t, err)
	}

	// List with defaults (should return all, newest first)
	result := store.List(ListOptions{})
	require.Equal(t, 5, result.Total)
	require.Len(t, result.Entries, 5)
	require.Equal(t, "task-e", result.Entries[0].TaskID) // Newest first

	// Test pagination
	result = store.List(ListOptions{Page: 1, Limit: 2})
	require.Equal(t, 5, result.Total)
	require.Equal(t, 3, result.TotalPages)
	require.Len(t, result.Entries, 2)
	require.Equal(t, "task-e", result.Entries[0].TaskID)
	require.Equal(t, "task-d", result.Entries[1].TaskID)

	// Page 2
	result = store.List(ListOptions{Page: 2, Limit: 2})
	require.Len(t, result.Entries, 2)
	require.Equal(t, "task-c", result.Entries[0].TaskID)
}

func TestStore_Pruning(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	// Create MaxOutlineEntries + 5 entries
	for i := 0; i < MaxOutlineEntries+5; i++ {
		entry := &Entry{
			TaskID:      "task-" + itoa(i),
			CompletedAt: time.Now().Add(time.Duration(i) * time.Minute),
		}
		err = store.Save(entry)
		require.NoError(t, err)
	}

	// Should have pruned to MaxOutlineEntries
	result := store.List(ListOptions{Limit: 200})
	require.Equal(t, MaxOutlineEntries, result.Total)

	// Oldest entries should be gone
	_, err = store.Get("task-0")
	require.Error(t, err)
	_, err = store.Get("task-4")
	require.Error(t, err)

	// Newest entries should still exist
	_, err = store.Get("task-" + itoa(MaxOutlineEntries+4))
	require.NoError(t, err)
}

func TestStore_DebugPruning(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	// Create MaxDebugEntries + 5 entries with debug logs
	for i := 0; i < MaxDebugEntries+5; i++ {
		entry := &Entry{
			TaskID:      "task-" + itoa(i),
			CompletedAt: time.Now().Add(time.Duration(i) * time.Minute),
		}
		err = store.Save(entry)
		require.NoError(t, err)

		err = store.SaveDebugLog(entry.TaskID, []byte("debug data"))
		require.NoError(t, err)
	}

	// Oldest debug logs should be pruned, but entries should remain
	for i := 0; i < 5; i++ {
		taskID := "task-" + itoa(i)
		entry, err := store.Get(taskID)
		require.NoError(t, err)
		require.False(t, entry.HasDebugLog, "old debug log should be pruned for %s", taskID)

		_, err = store.GetDebugLog(taskID)
		require.Error(t, err)
	}

	// Newest debug logs should still exist
	for i := MaxDebugEntries + 5 - MaxDebugEntries; i < MaxDebugEntries+5; i++ {
		taskID := "task-" + itoa(i)
		entry, err := store.Get(taskID)
		require.NoError(t, err)
		require.True(t, entry.HasDebugLog, "recent debug log should exist for %s", taskID)
	}
}

func TestStore_Load(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create store and add entries
	store1, err := NewStore(dir)
	require.NoError(t, err)

	entry := &Entry{
		TaskID:      "task-persist",
		SessionID:   "session-abc",
		Prompt:      "Test persistence",
		CompletedAt: time.Now(),
	}
	err = store1.Save(entry)
	require.NoError(t, err)

	err = store1.SaveDebugLog("task-persist", []byte("debug"))
	require.NoError(t, err)

	// Create new store from same directory (simulates restart)
	store2, err := NewStore(dir)
	require.NoError(t, err)

	// Entry should be loaded
	got, err := store2.Get("task-persist")
	require.NoError(t, err)
	require.Equal(t, "task-persist", got.TaskID)
	require.True(t, got.HasDebugLog)
}

func TestStore_NotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	_, err = store.Get("nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")

	_, err = store.GetDebugLog("nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

// itoa is a simple int to string converter for test task IDs
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	result := ""
	for i > 0 {
		result = string(rune('0'+i%10)) + result
		i /= 10
	}
	return result
}
