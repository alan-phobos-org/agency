package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQueueAdd(t *testing.T) {
	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	task, pos, err := q.Add(QueueSubmitRequest{Prompt: "test"})
	require.NoError(t, err)
	require.Equal(t, 1, pos)
	require.Equal(t, TaskStatePending, task.State)
	require.NotEmpty(t, task.QueueID)
}

func TestQueueFIFO(t *testing.T) {
	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	q.Add(QueueSubmitRequest{Prompt: "first"})
	q.Add(QueueSubmitRequest{Prompt: "second"})
	q.Add(QueueSubmitRequest{Prompt: "third"})

	task := q.NextPending()
	require.NotNil(t, task)
	require.Equal(t, "first", task.Prompt)
}

func TestQueueMaxSize(t *testing.T) {
	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 2,
	})
	require.NoError(t, err)

	_, _, err = q.Add(QueueSubmitRequest{Prompt: "1"})
	require.NoError(t, err)
	_, _, err = q.Add(QueueSubmitRequest{Prompt: "2"})
	require.NoError(t, err)
	_, _, err = q.Add(QueueSubmitRequest{Prompt: "3"})
	require.ErrorIs(t, err, ErrQueueFull)
}

func TestQueuePersistence(t *testing.T) {
	dir := t.TempDir()

	// Add task
	q1, err := NewWorkQueue(QueueConfig{
		Dir:     dir,
		MaxSize: 50,
	})
	require.NoError(t, err)
	q1.Add(QueueSubmitRequest{Prompt: "persistent"})

	// Reload from disk
	q2, err := NewWorkQueue(QueueConfig{
		Dir:     dir,
		MaxSize: 50,
	})
	require.NoError(t, err)
	require.Equal(t, 1, q2.Depth())

	task := q2.NextPending()
	require.NotNil(t, task)
	require.Equal(t, "persistent", task.Prompt)
}

func TestQueueCancel(t *testing.T) {
	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	task, _, _ := q.Add(QueueSubmitRequest{Prompt: "to cancel"})

	cancelled, ok := q.Cancel(task.QueueID)
	require.True(t, ok)
	require.Equal(t, TaskStateCancelled, cancelled.State)
	require.Equal(t, 0, q.Depth())
}

func TestQueueSetDispatched(t *testing.T) {
	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	task, _, _ := q.Add(QueueSubmitRequest{Prompt: "test"})

	q.SetDispatched(task, "http://agent:9000", "task-123", "session-456")

	require.Equal(t, TaskStateWorking, task.State)
	require.Equal(t, "http://agent:9000", task.AgentURL)
	require.Equal(t, "task-123", task.TaskID)
	require.Equal(t, "session-456", task.SessionID)
	require.NotNil(t, task.DispatchedAt)
}

func TestQueueRequeueAtBack(t *testing.T) {
	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	task1, _, _ := q.Add(QueueSubmitRequest{Prompt: "first"})
	q.Add(QueueSubmitRequest{Prompt: "second"})

	// Requeue first task at back
	q.RequeueAtBack(task1)

	// Now second should be first
	next := q.NextPending()
	require.Equal(t, "second", next.Prompt)
}

func TestQueuePosition(t *testing.T) {
	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	task1, _, _ := q.Add(QueueSubmitRequest{Prompt: "first"})
	task2, _, _ := q.Add(QueueSubmitRequest{Prompt: "second"})
	task3, _, _ := q.Add(QueueSubmitRequest{Prompt: "third"})

	require.Equal(t, 1, q.Position(task1.QueueID))
	require.Equal(t, 2, q.Position(task2.QueueID))
	require.Equal(t, 3, q.Position(task3.QueueID))
}

func TestQueueOldestAge(t *testing.T) {
	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	require.Equal(t, 0.0, q.OldestAge())

	q.Add(QueueSubmitRequest{Prompt: "test"})
	time.Sleep(10 * time.Millisecond)

	age := q.OldestAge()
	require.Greater(t, age, 0.0)
}

func TestQueueDepthOnlyCountsPending(t *testing.T) {
	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	task, _, _ := q.Add(QueueSubmitRequest{Prompt: "test1"})
	q.Add(QueueSubmitRequest{Prompt: "test2"})

	require.Equal(t, 2, q.Depth())

	// Mark one as dispatched
	q.SetDispatched(task, "http://agent:9000", "task-1", "")

	require.Equal(t, 1, q.Depth())
	require.Equal(t, 1, q.DispatchedCount())
}

func TestQueueRemove(t *testing.T) {
	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	task, _, _ := q.Add(QueueSubmitRequest{Prompt: "test"})
	require.Equal(t, 1, q.Depth())

	q.Remove(task)
	require.Equal(t, 0, q.Depth())
	require.Nil(t, q.Get(task.QueueID))
}

func TestQueueSourceTracking(t *testing.T) {
	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	task, _, _ := q.Add(QueueSubmitRequest{
		Prompt:    "test",
		Source:    "scheduler",
		SourceJob: "nightly-job",
	})

	require.Equal(t, "scheduler", task.Source)
	require.Equal(t, "nightly-job", task.SourceJob)
}
