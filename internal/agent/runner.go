package agent

import "phobos.org.uk/agency/internal/config"

// RunnerCommand describes how to invoke a CLI runner.
type RunnerCommand struct {
	Args          []string
	PromptInStdin bool
}

// RunnerOutput captures parsed CLI output.
type RunnerOutput struct {
	SessionID        string
	Output           string
	ExitCode         int
	TokenUsage       *TokenUsage
	MaxTurnsExceeded bool
	HasOutput        bool
}

// Runner defines a provider-specific CLI adapter.
type Runner interface {
	Kind() string
	ResolveBin() string
	BuildCommand(task *Task, prompt string, cfg *config.Config) RunnerCommand
	ParseOutput(stdout []byte) (RunnerOutput, bool)
	ErrorType() string
	SupportsAutoResume() bool
	MaxTurnsLimit(cfg *config.Config) int
}

// NewClaudeRunner returns a Claude CLI runner.
func NewClaudeRunner() Runner {
	return claudeRunner{}
}

// NewCodexRunner returns a Codex CLI runner.
func NewCodexRunner() Runner {
	return codexRunner{}
}
