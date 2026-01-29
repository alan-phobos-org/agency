package taskstate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateString(t *testing.T) {
	assert.Equal(t, "working", Working.String())
	assert.Equal(t, "completed", Completed.String())
	assert.Equal(t, "pending", Pending.String())
}

func TestIsTerminal(t *testing.T) {
	tests := []struct {
		state    State
		terminal bool
	}{
		{Queued, false},
		{Pending, false},
		{Dispatching, false},
		{Working, false},
		{Completed, true},
		{Failed, true},
		{Cancelled, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			assert.Equal(t, tt.terminal, tt.state.IsTerminal())
		})
	}
}

func TestIsActive(t *testing.T) {
	tests := []struct {
		state  State
		active bool
	}{
		{Queued, true},
		{Pending, true},
		{Dispatching, true},
		{Working, true},
		{Completed, false},
		{Failed, false},
		{Cancelled, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			assert.Equal(t, tt.active, tt.state.IsActive())
		})
	}
}

func TestIsPending(t *testing.T) {
	assert.True(t, Queued.IsPending())
	assert.True(t, Pending.IsPending())
	assert.False(t, Working.IsPending())
	assert.False(t, Completed.IsPending())
}

func TestIsDispatched(t *testing.T) {
	assert.False(t, Pending.IsDispatched())
	assert.True(t, Dispatching.IsDispatched())
	assert.True(t, Working.IsDispatched())
	assert.False(t, Completed.IsDispatched())
}

func TestCanTransition(t *testing.T) {
	// Valid transitions
	assert.True(t, CanTransition(Queued, Working))
	assert.True(t, CanTransition(Queued, Cancelled))
	assert.True(t, CanTransition(Pending, Dispatching))
	assert.True(t, CanTransition(Dispatching, Working))
	assert.True(t, CanTransition(Dispatching, Pending)) // Requeue
	assert.True(t, CanTransition(Working, Completed))
	assert.True(t, CanTransition(Working, Failed))
	assert.True(t, CanTransition(Working, Cancelled))

	// Invalid transitions
	assert.False(t, CanTransition(Completed, Working))
	assert.False(t, CanTransition(Failed, Completed))
	assert.False(t, CanTransition(Cancelled, Working))
	assert.False(t, CanTransition(Working, Pending)) // Can't go back to pending
	assert.False(t, CanTransition(Completed, Completed))
}

func TestTerminalStatesCannotTransition(t *testing.T) {
	for _, terminal := range TerminalStates() {
		for _, target := range AllStates() {
			assert.False(t, CanTransition(terminal, target),
				"terminal state %s should not transition to %s", terminal, target)
		}
	}
}

func TestAllStates(t *testing.T) {
	states := AllStates()
	require.Len(t, states, 7)

	// Check all expected states are present
	expected := map[State]bool{
		Queued:      false,
		Pending:     false,
		Dispatching: false,
		Working:     false,
		Completed:   false,
		Failed:      false,
		Cancelled:   false,
	}
	for _, s := range states {
		expected[s] = true
	}
	for s, found := range expected {
		assert.True(t, found, "state %s should be in AllStates()", s)
	}
}

func TestTerminalStates(t *testing.T) {
	terminals := TerminalStates()
	require.Len(t, terminals, 3)

	for _, s := range terminals {
		assert.True(t, s.IsTerminal())
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		input    string
		expected State
		valid    bool
	}{
		{"working", Working, true},
		{"completed", Completed, true},
		{"pending", Pending, true},
		{"dispatching", Dispatching, true},
		{"queued", Queued, true},
		{"failed", Failed, true},
		{"cancelled", Cancelled, true},
		{"invalid", "", false},
		{"", "", false},
		{"WORKING", "", false}, // Case sensitive
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			state, valid := Parse(tt.input)
			assert.Equal(t, tt.valid, valid)
			if valid {
				assert.Equal(t, tt.expected, state)
			}
		})
	}
}
