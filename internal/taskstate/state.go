// Package taskstate provides a unified state machine for task lifecycle management.
// It defines all valid task states and transitions across the agency system.
package taskstate

// State represents a task's current state in its lifecycle.
type State string

// Task states shared across agent and queue systems.
// States are designed to be compatible with both direct agent execution
// and queue-based dispatch workflows.
const (
	// Queued indicates a task has been created but not yet started.
	// Used by agents when a task is accepted but execution hasn't begun.
	Queued State = "queued"

	// Pending indicates a task is waiting in the queue for dispatch.
	// Used by the work queue for tasks awaiting an available agent.
	Pending State = "pending"

	// Dispatching indicates a task is being sent to an agent.
	// Transient state during the handoff from queue to agent.
	Dispatching State = "dispatching"

	// Working indicates a task is actively being executed.
	Working State = "working"

	// Completed indicates a task finished successfully.
	Completed State = "completed"

	// Failed indicates a task finished with an error.
	Failed State = "failed"

	// Cancelled indicates a task was cancelled by the user.
	Cancelled State = "cancelled"
)

// String returns the string representation of the state.
func (s State) String() string {
	return string(s)
}

// IsTerminal returns true if the state is a final state (no further transitions).
func (s State) IsTerminal() bool {
	switch s {
	case Completed, Failed, Cancelled:
		return true
	}
	return false
}

// IsActive returns true if the state indicates the task is in progress.
func (s State) IsActive() bool {
	switch s {
	case Queued, Pending, Dispatching, Working:
		return true
	}
	return false
}

// IsPending returns true if the state is a waiting state (not yet executing).
// This includes both Queued (agent-side: accepted but not started) and
// Pending (queue-side: waiting for agent dispatch).
func (s State) IsPending() bool {
	switch s {
	case Queued, Pending:
		return true
	}
	return false
}

// IsDispatched returns true if the task has been dispatched to an agent.
func (s State) IsDispatched() bool {
	switch s {
	case Dispatching, Working:
		return true
	}
	return false
}

// ValidTransitions defines the allowed state transitions.
// Each state maps to the set of states it can transition to.
var ValidTransitions = map[State][]State{
	Queued:      {Working, Cancelled, Failed},
	Pending:     {Dispatching, Cancelled, Failed},
	Dispatching: {Working, Pending, Failed, Cancelled},
	Working:     {Completed, Failed, Cancelled},
	Completed:   {}, // Terminal
	Failed:      {}, // Terminal
	Cancelled:   {}, // Terminal
}

// CanTransition returns true if transitioning from 'from' to 'to' is valid.
func CanTransition(from, to State) bool {
	allowed, ok := ValidTransitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// AllStates returns all defined states.
func AllStates() []State {
	return []State{
		Queued,
		Pending,
		Dispatching,
		Working,
		Completed,
		Failed,
		Cancelled,
	}
}

// TerminalStates returns all terminal states.
func TerminalStates() []State {
	return []State{Completed, Failed, Cancelled}
}

// Parse converts a string to a State, returning the state and whether it was valid.
func Parse(s string) (State, bool) {
	state := State(s)
	for _, valid := range AllStates() {
		if state == valid {
			return state, true
		}
	}
	return "", false
}
