// Package api defines shared types and constants for the agency framework.
package api

// Component types identify the kind of component.
const (
	TypeAgent    = "agent"
	TypeDirector = "director"
	TypeHelper   = "helper"
	TypeView     = "view"
)

// Agent kinds identify which runtime an agent uses.
const (
	AgentKindClaude = "claude"
	AgentKindCodex  = "codex"
)

// Tier names identify model selection tiers.
const (
	TierFast     = "fast"
	TierStandard = "standard"
	TierHeavy    = "heavy"
)

// Interface names identify component capabilities.
const (
	InterfaceStatusable   = "statusable"
	InterfaceTaskable     = "taskable"
	InterfaceObservable   = "observable"
	InterfaceConfigurable = "configurable"
)

// Error codes for consistent API error responses.
const (
	// Agent errors
	ErrorAgentBusy        = "agent_busy"
	ErrorAlreadyCompleted = "already_completed"
	ErrorTaskInProgress   = "task_in_progress"

	// Resource errors
	ErrorNotFound    = "not_found"
	ErrorJobNotFound = "job_not_found"

	// State errors
	ErrorJobAlreadyRunning = "job_already_running"

	// Auth errors
	ErrorUnauthorized = "unauthorized"

	// Generic errors
	ErrorReadError = "read_error"
)

// ProjectContext provides project-specific instructions prepended to task prompts.
type ProjectContext struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

// IsValidTier returns true if the tier name is known.
func IsValidTier(tier string) bool {
	switch tier {
	case TierFast, TierStandard, TierHeavy:
		return true
	default:
		return false
	}
}
