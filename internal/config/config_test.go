package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Parallel()

	// HistoryDir is derived dynamically, so compute expected value
	expectedHistoryDir := DefaultHistoryPath(DefaultName)

	tests := []struct {
		name    string
		yaml    string
		want    *Config
		wantErr string
	}{
		{
			name: "minimal config",
			yaml: "port: 9000",
			want: &Config{
				Port:       9000,
				Name:       DefaultName,
				LogLevel:   DefaultLogLevel,
				SessionDir: DefaultSessionDir,
				HistoryDir: expectedHistoryDir,
				AgentKind:  DefaultAgentKind,
				Claude: ClaudeConfig{
					Model:    DefaultModel,
					Timeout:  DefaultTimeout,
					MaxTurns: DefaultMaxTurns,
				},
				Codex: CodexConfig{
					Model:   DefaultCodexModel,
					Timeout: DefaultCodexTimeout,
				},
			},
		},
		{
			name: "full config",
			yaml: `
port: 9001
log_level: debug
claude:
  model: opus
  timeout: 1h
`,
			want: &Config{
				Port:       9001,
				Name:       DefaultName,
				LogLevel:   "debug",
				SessionDir: DefaultSessionDir,
				HistoryDir: expectedHistoryDir,
				AgentKind:  DefaultAgentKind,
				Claude: ClaudeConfig{
					Model:    "opus",
					Timeout:  time.Hour,
					MaxTurns: DefaultMaxTurns,
				},
				Codex: CodexConfig{
					Model:   DefaultCodexModel,
					Timeout: DefaultCodexTimeout,
				},
			},
		},
		{
			name:    "invalid port zero",
			yaml:    "port: 0",
			wantErr: "port must be between 1 and 65535",
		},
		{
			name:    "invalid port too high",
			yaml:    "port: 70000",
			wantErr: "port must be between 1 and 65535",
		},
		{
			name: "invalid model",
			yaml: `
port: 9000
claude:
  model: gpt4
`,
			wantErr: "model must be opus, sonnet, or haiku",
		},
		{
			name: "invalid timeout",
			yaml: `
port: 9000
claude:
  timeout: 100ms
`,
			wantErr: "timeout must be at least 1 second",
		},
		{
			name: "invalid max_turns",
			yaml: `
port: 9000
claude:
  max_turns: 0
`,
			wantErr: "max_turns must be at least 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Parse([]byte(tt.yaml))

			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestDefault(t *testing.T) {
	t.Parallel()

	cfg := Default()
	require.Equal(t, DefaultPort, cfg.Port)
	require.Equal(t, DefaultName, cfg.Name)
	require.Equal(t, DefaultLogLevel, cfg.LogLevel)
	require.Equal(t, DefaultSessionDir, cfg.SessionDir)
	require.Equal(t, DefaultHistoryPath(DefaultName), cfg.HistoryDir)
	require.Equal(t, DefaultAgentKind, cfg.AgentKind)
	require.Equal(t, DefaultModel, cfg.Claude.Model)
	require.Equal(t, DefaultTimeout, cfg.Claude.Timeout)
	require.Equal(t, DefaultMaxTurns, cfg.Claude.MaxTurns)
	require.Equal(t, DefaultCodexModel, cfg.Codex.Model)
	require.Equal(t, DefaultCodexTimeout, cfg.Codex.Timeout)
}
