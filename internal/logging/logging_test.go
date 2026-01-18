package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogger_BasicLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Config{
		Output:    &buf,
		Level:     LevelDebug,
		Component: "test",
	})

	logger.Debug("debug message")
	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 4)

	// Verify each line is valid JSON with expected fields
	for i, line := range lines {
		var entry Entry
		err := json.Unmarshal([]byte(line), &entry)
		require.NoError(t, err, "line %d should be valid JSON", i)
		assert.Equal(t, "test", entry.Component)
		assert.False(t, entry.Timestamp.IsZero())
	}

	// Check levels
	var entry Entry
	json.Unmarshal([]byte(lines[0]), &entry)
	assert.Equal(t, LevelDebug, entry.Level)
	assert.Equal(t, "debug message", entry.Message)

	json.Unmarshal([]byte(lines[3]), &entry)
	assert.Equal(t, LevelError, entry.Level)
	assert.Equal(t, "error message", entry.Message)
}

func TestLogger_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Config{
		Output: &buf,
		Level:  LevelWarn,
	})

	logger.Debug("should not appear")
	logger.Info("should not appear")
	logger.Warn("should appear")
	logger.Error("should appear")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Len(t, lines, 2)

	var entry Entry
	json.Unmarshal([]byte(lines[0]), &entry)
	assert.Equal(t, LevelWarn, entry.Level)
}

func TestLogger_WithFields(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Config{
		Output: &buf,
		Level:  LevelInfo,
	})

	logger.Info("with fields", map[string]any{
		"key1": "value1",
		"key2": 42,
	})

	var entry Entry
	err := json.Unmarshal(buf.Bytes(), &entry)
	require.NoError(t, err)
	assert.Equal(t, "value1", entry.Fields["key1"])
	assert.Equal(t, float64(42), entry.Fields["key2"]) // JSON numbers are float64
}

func TestLogger_TaskLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Config{
		Output:    &buf,
		Level:     LevelInfo,
		Component: "agent",
	})

	taskLog := logger.WithTask("task-123")
	taskLog.Info("task started")
	taskLog.Error("task failed")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 2)

	var entry Entry
	json.Unmarshal([]byte(lines[0]), &entry)
	assert.Equal(t, "task-123", entry.TaskID)
	assert.Equal(t, "agent", entry.Component)
	assert.Equal(t, LevelInfo, entry.Level)

	json.Unmarshal([]byte(lines[1]), &entry)
	assert.Equal(t, "task-123", entry.TaskID)
	assert.Equal(t, LevelError, entry.Level)
}

func TestLogger_Stats(t *testing.T) {
	logger := New(Config{
		Output: &bytes.Buffer{},
		Level:  LevelDebug,
	})

	logger.Debug("d1")
	logger.Debug("d2")
	logger.Info("i1")
	logger.Warn("w1")
	logger.Error("e1")
	logger.Error("e2")
	logger.Error("e3")

	stats := logger.Stats()
	assert.Equal(t, int64(2), stats.Debug)
	assert.Equal(t, int64(1), stats.Info)
	assert.Equal(t, int64(1), stats.Warn)
	assert.Equal(t, int64(3), stats.Error)
	assert.Equal(t, int64(7), stats.Total)
}

func TestLogger_Query(t *testing.T) {
	logger := New(Config{
		Output:    &bytes.Buffer{},
		Level:     LevelDebug,
		Component: "test",
	})

	// Add entries with different levels and tasks
	logger.Debug("debug entry")
	logger.Info("info entry")
	taskLog := logger.WithTask("task-1")
	taskLog.Warn("task warning")
	taskLog.Error("task error")
	logger.Error("general error")

	t.Run("no filter returns all", func(t *testing.T) {
		result := logger.Query(Query{})
		assert.Len(t, result.Entries, 5)
		assert.Equal(t, 5, result.Total)
	})

	t.Run("filter by level", func(t *testing.T) {
		result := logger.Query(Query{Level: LevelWarn})
		assert.Len(t, result.Entries, 3) // 1 warn + 2 errors
		for _, e := range result.Entries {
			assert.True(t, e.Level == LevelWarn || e.Level == LevelError)
		}
	})

	t.Run("filter by task", func(t *testing.T) {
		result := logger.Query(Query{TaskID: "task-1"})
		assert.Len(t, result.Entries, 2)
		for _, e := range result.Entries {
			assert.Equal(t, "task-1", e.TaskID)
		}
	})

	t.Run("filter by level and task", func(t *testing.T) {
		result := logger.Query(Query{Level: LevelError, TaskID: "task-1"})
		assert.Len(t, result.Entries, 1)
		assert.Equal(t, "task error", result.Entries[0].Message)
	})

	t.Run("limit", func(t *testing.T) {
		result := logger.Query(Query{Limit: 2})
		assert.Len(t, result.Entries, 2)
		assert.Equal(t, 5, result.Total) // Total before limit
		// Should return most recent
		assert.Equal(t, "general error", result.Entries[1].Message)
	})
}

func TestLogger_QueryTimeFilter(t *testing.T) {
	logger := New(Config{
		Output: &bytes.Buffer{},
		Level:  LevelInfo,
	})

	// Add entries with slight time differences
	logger.Info("entry 1")
	time.Sleep(10 * time.Millisecond)
	midpoint := time.Now().UTC()
	time.Sleep(10 * time.Millisecond)
	logger.Info("entry 2")
	logger.Info("entry 3")

	t.Run("since filter", func(t *testing.T) {
		result := logger.Query(Query{Since: midpoint})
		assert.Len(t, result.Entries, 2)
	})

	t.Run("until filter", func(t *testing.T) {
		result := logger.Query(Query{Until: midpoint})
		assert.Len(t, result.Entries, 1)
	})
}

func TestLogger_RingBuffer(t *testing.T) {
	logger := New(Config{
		Output:     &bytes.Buffer{},
		Level:      LevelInfo,
		MaxEntries: 3,
	})

	logger.Info("entry 1")
	logger.Info("entry 2")
	logger.Info("entry 3")
	logger.Info("entry 4") // Should push out entry 1
	logger.Info("entry 5") // Should push out entry 2

	result := logger.Query(Query{})
	require.Len(t, result.Entries, 3)
	assert.Equal(t, "entry 3", result.Entries[0].Message)
	assert.Equal(t, "entry 4", result.Entries[1].Message)
	assert.Equal(t, "entry 5", result.Entries[2].Message)

	// But counts should still reflect all entries
	stats := logger.Stats()
	assert.Equal(t, int64(5), stats.Info)
}

func TestLogger_Clear(t *testing.T) {
	logger := New(Config{
		Output: &bytes.Buffer{},
		Level:  LevelInfo,
	})

	logger.Info("entry 1")
	logger.Error("entry 2")

	stats := logger.Stats()
	assert.Equal(t, int64(2), stats.Total)

	logger.Clear()

	stats = logger.Stats()
	assert.Equal(t, int64(0), stats.Total)

	result := logger.Query(Query{})
	assert.Len(t, result.Entries, 0)
}

func TestLogger_SetLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Config{
		Output: &buf,
		Level:  LevelError,
	})

	logger.Info("should not appear")
	assert.Empty(t, buf.String())

	logger.SetLevel(LevelInfo)
	logger.Info("should appear")
	assert.Contains(t, buf.String(), "should appear")
}

func TestLogger_Concurrency(t *testing.T) {
	logger := New(Config{
		Output:     &bytes.Buffer{},
		Level:      LevelDebug,
		MaxEntries: 100,
	})

	// Concurrent writes
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				logger.Info("message", map[string]any{"goroutine": id, "iteration": j})
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should have 1000 total logged
	stats := logger.Stats()
	assert.Equal(t, int64(1000), stats.Info)

	// Buffer should have max entries
	result := logger.Query(Query{})
	assert.Len(t, result.Entries, 100)
}
