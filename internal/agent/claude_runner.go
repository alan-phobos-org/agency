package agent

import (
	"encoding/json"
	"os"
	"strconv"

	"phobos.org.uk/agency/internal/api"
	"phobos.org.uk/agency/internal/config"
)

type claudeRunner struct{}

func (claudeRunner) Kind() string {
	return api.AgentKindClaude
}

func (claudeRunner) ResolveBin() string {
	claudeBin := os.Getenv("CLAUDE_BIN")
	if claudeBin == "" {
		claudeBin = "claude"
	}
	return claudeBin
}

func (claudeRunner) BuildCommand(task *Task, prompt string, cfg *config.Config) RunnerCommand {
	args := []string{
		"--print",
		"--dangerously-skip-permissions",
		"--model", task.Model,
		"--output-format", "json",
		"--max-turns", strconv.Itoa(cfg.Claude.MaxTurns),
	}

	// Add session handling for conversation continuity
	// For new sessions: pass --session-id to create session with our UUID
	// For resumed sessions: pass --resume to continue the existing session
	if task.SessionID != "" {
		if task.ResumeSession {
			args = append(args, "--resume", task.SessionID)
		} else {
			args = append(args, "--session-id", task.SessionID)
		}
	}

	// Use "--" to prevent prompt being parsed as flags.
	args = append(args, "-p", "--", prompt)
	return RunnerCommand{Args: args}
}

func (claudeRunner) ParseOutput(stdout []byte) (RunnerOutput, bool) {
	var resp struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id"`
		Result    string `json:"result"`
		ExitCode  int    `json:"exit_code"`
		Usage     struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(stdout, &resp); err != nil {
		return RunnerOutput{}, false
	}

	out := RunnerOutput{
		SessionID: resp.SessionID,
		Output:    resp.Result,
		ExitCode:  resp.ExitCode,
		TokenUsage: &TokenUsage{
			Input:  resp.Usage.InputTokens,
			Output: resp.Usage.OutputTokens,
		},
		MaxTurnsExceeded: resp.Subtype == "error_max_turns",
		HasOutput:        true,
	}
	return out, true
}

func (claudeRunner) ErrorType() string {
	return "claude_error"
}

func (claudeRunner) SupportsAutoResume() bool {
	return true
}

func (claudeRunner) MaxTurnsLimit(cfg *config.Config) int {
	return cfg.Claude.MaxTurns
}
