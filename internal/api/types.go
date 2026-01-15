// Package api defines shared types and constants for the agency framework.
package api

// Component types identify the kind of component.
const (
	TypeAgent    = "agent"
	TypeDirector = "director"
	TypeHelper   = "helper"
	TypeView     = "view"
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
	ErrorRateLimited  = "rate_limited"

	// Generic errors
	ErrorReadError = "read_error"
)

// ProjectContext provides project-specific instructions prepended to task prompts.
type ProjectContext struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}
