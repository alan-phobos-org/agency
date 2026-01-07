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

// ProjectContext provides project-specific instructions prepended to task prompts.
type ProjectContext struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}
