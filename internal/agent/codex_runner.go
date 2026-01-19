package agent

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"os"
	"strings"

	"phobos.org.uk/agency/internal/api"
	"phobos.org.uk/agency/internal/config"
)

//go:embed codex.md
var agentCodexMD string

type codexRunner struct{}

func (codexRunner) Kind() string {
	return api.AgentKindCodex
}

func (codexRunner) ResolveBin() string {
	codexBin := os.Getenv("CODEX_BIN")
	if codexBin == "" {
		codexBin = "codex"
	}
	return codexBin
}

func (codexRunner) DefaultPreprompt() string {
	return agentCodexMD
}

func (codexRunner) BuildCommand(task *Task, prompt string, cfg *config.Config) RunnerCommand {
	args := []string{
		"exec",
		"--dangerously-bypass-approvals-and-sandbox",
		"--json",
		"--skip-git-repo-check",
	}

	if task.Model != "" {
		args = append(args, "--model", task.Model)
	}

	if task.ResumeSession && task.SessionID != "" {
		args = append(args, "resume", task.SessionID, "-")
	} else {
		args = append(args, "-")
	}

	return RunnerCommand{
		Args:          args,
		PromptInStdin: true,
	}
}

func (codexRunner) ParseOutput(stdout []byte) (RunnerOutput, bool) {
	var out RunnerOutput
	parsed := false
	var lastOutput string
	hasOutput := false

	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		parsed = true

		if sid, ok := raw["session_id"].(string); ok && sid != "" {
			out.SessionID = sid
		}

		if usageRaw, ok := raw["usage"].(map[string]interface{}); ok {
			inputTokens := intFromAny(usageRaw["input_tokens"])
			outputTokens := intFromAny(usageRaw["output_tokens"])
			if inputTokens == 0 {
				inputTokens = intFromAny(usageRaw["prompt_tokens"])
			}
			if outputTokens == 0 {
				outputTokens = intFromAny(usageRaw["completion_tokens"])
			}
			if inputTokens > 0 || outputTokens > 0 {
				out.TokenUsage = &TokenUsage{Input: inputTokens, Output: outputTokens}
			}
		}

		if exitCode := intFromAny(raw["exit_code"]); exitCode != 0 {
			out.ExitCode = exitCode
		}

		if text, ok := extractOutputText(raw); ok {
			lastOutput = text
			hasOutput = true
		}
	}

	if parsed {
		out.Output = lastOutput
		out.HasOutput = hasOutput
		return out, true
	}
	return RunnerOutput{}, false
}

func (codexRunner) ErrorType() string {
	return "codex_error"
}

func (codexRunner) SupportsAutoResume() bool {
	return false
}

func (codexRunner) MaxTurnsLimit(cfg *config.Config) int {
	return 0
}

func extractOutputText(raw map[string]interface{}) (string, bool) {
	if v, ok := raw["result"].(string); ok {
		return v, true
	}
	if v, ok := raw["output"].(string); ok {
		return v, true
	}
	if v, ok := raw["text"].(string); ok {
		return v, true
	}
	if v, ok := raw["content"].(string); ok {
		return v, true
	}
	// Handle codex CLI format: {"type":"item.completed","item":{"type":"agent_message","text":"..."}}
	if item, ok := raw["item"].(map[string]interface{}); ok {
		if itemType, ok := item["type"].(string); ok && itemType == "agent_message" {
			if text, ok := item["text"].(string); ok {
				return text, true
			}
		}
	}
	if message, ok := raw["message"]; ok {
		return extractMessageContent(message)
	}
	return "", false
}

func extractMessageContent(message interface{}) (string, bool) {
	switch v := message.(type) {
	case string:
		return v, true
	case map[string]interface{}:
		if content, ok := v["content"]; ok {
			return extractContentText(content)
		}
	}
	return "", false
}

func extractContentText(content interface{}) (string, bool) {
	switch v := content.(type) {
	case string:
		return v, true
	case []interface{}:
		var parts []string
		for _, item := range v {
			switch piece := item.(type) {
			case map[string]interface{}:
				if text, ok := piece["text"].(string); ok {
					parts = append(parts, text)
				} else if text, ok := piece["content"].(string); ok {
					parts = append(parts, text)
				}
			case string:
				parts = append(parts, piece)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, ""), true
		}
	}
	return "", false
}

func intFromAny(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	}
	return 0
}
