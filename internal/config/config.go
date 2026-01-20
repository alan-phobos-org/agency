package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
	"phobos.org.uk/agency/internal/api"
)

// Config represents the agent configuration
type Config struct {
	Port          int             `yaml:"port"`
	Name          string          `yaml:"name"` // Agent name (used for history directory)
	LogLevel      string          `yaml:"log_level"`
	SessionDir    string          `yaml:"session_dir"`    // Base directory for session workspaces
	HistoryDir    string          `yaml:"history_dir"`    // Directory for task history storage
	PrepromptFile string          `yaml:"preprompt_file"` // Optional path to custom preprompt file
	AgentKind     string          `yaml:"agent_kind"`     // claude, codex
	Tiers         TierConfig      `yaml:"tiers"`
	Claude        ClaudeConfig    `yaml:"claude"`
	Codex         CodexConfig     `yaml:"codex"`
	Projects      []ProjectConfig `yaml:"projects,omitempty"`
}

// ProjectConfig defines a project context that can be prepended to task prompts
type ProjectConfig struct {
	Name   string `yaml:"name"`
	Prompt string `yaml:"prompt"`
}

// ClaudeConfig holds Claude CLI settings
type ClaudeConfig struct {
	Model    string        `yaml:"model"`
	Timeout  time.Duration `yaml:"timeout"`
	MaxTurns int           `yaml:"max_turns"` // Maximum conversation turns per execution (default: 50)
}

// CodexConfig holds Codex CLI settings.
type CodexConfig struct {
	Model   string        `yaml:"model"`
	Timeout time.Duration `yaml:"timeout"`
}

// TierConfig holds model tier mappings.
type TierConfig struct {
	Fast     string `yaml:"fast"`
	Standard string `yaml:"standard"`
	Heavy    string `yaml:"heavy"`
}

// HasAny reports whether any tier mapping is set.
func (t TierConfig) HasAny() bool {
	return t.Fast != "" || t.Standard != "" || t.Heavy != ""
}

// Value returns the model name for a tier.
func (t TierConfig) Value(tier string) string {
	switch tier {
	case api.TierFast:
		return t.Fast
	case api.TierStandard:
		return t.Standard
	case api.TierHeavy:
		return t.Heavy
	default:
		return ""
	}
}

// DefaultClaudeTiers returns the default tier mapping for Claude agents.
func DefaultClaudeTiers() TierConfig {
	return TierConfig{
		Fast:     "haiku",
		Standard: "sonnet",
		Heavy:    "opus",
	}
}

// DefaultCodexTiers returns the default tier mapping for Codex (OpenAI) agents.
func DefaultCodexTiers() TierConfig {
	return TierConfig{
		Fast:     "gpt-5.1-codex-mini",
		Standard: "gpt-5.2-codex",
		Heavy:    "gpt-5.1-codex-max",
	}
}

// Defaults
const (
	DefaultPort         = 9000
	DefaultName         = "agent"
	DefaultModel        = "sonnet"
	DefaultTimeout      = 30 * time.Minute
	DefaultMaxTurns     = 50
	DefaultLogLevel     = "info"
	DefaultSessionDir   = "" // Derived from AGENCY_ROOT or ~/.agency/sessions
	DefaultHistoryDir   = "" // Derived from AGENCY_ROOT or ~/.agency/history/<name>
	DefaultAgentKind    = api.AgentKindClaude
	DefaultCodexModel   = ""
	DefaultCodexTimeout = 30 * time.Minute
)

// Parse parses YAML config data
func Parse(data []byte) (*Config, error) {
	cfg := &Config{
		Port:       DefaultPort,
		Name:       DefaultName,
		LogLevel:   DefaultLogLevel,
		SessionDir: DefaultSessionDir,
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
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Derive SessionDir if not set
	if cfg.SessionDir == "" {
		cfg.SessionDir = DefaultSessionPath()
	}

	// Derive HistoryDir if not set
	if cfg.HistoryDir == "" {
		cfg.HistoryDir = DefaultHistoryPath(cfg.Name)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Load loads config from a file path
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}
	return Parse(data)
}

// Validate checks config validity
func (c *Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", c.Port)
	}

	switch c.AgentKind {
	case api.AgentKindClaude, api.AgentKindCodex:
	default:
		return fmt.Errorf("agent_kind must be claude or codex, got %q", c.AgentKind)
	}

	if c.AgentKind == api.AgentKindClaude {
		validModels := map[string]bool{"opus": true, "sonnet": true, "haiku": true}
		if !validModels[c.Claude.Model] {
			return fmt.Errorf("model must be opus, sonnet, or haiku, got %q", c.Claude.Model)
		}

		if c.Claude.Timeout < time.Second {
			return fmt.Errorf("timeout must be at least 1 second, got %v", c.Claude.Timeout)
		}

		if c.Claude.MaxTurns < 1 {
			return fmt.Errorf("max_turns must be at least 1, got %d", c.Claude.MaxTurns)
		}
	}

	if c.AgentKind == api.AgentKindCodex {
		if c.Codex.Timeout < time.Second {
			return fmt.Errorf("codex timeout must be at least 1 second, got %v", c.Codex.Timeout)
		}
	}

	return nil
}

// Default returns a config with default values
func Default() *Config {
	return &Config{
		Port:       DefaultPort,
		Name:       DefaultName,
		LogLevel:   DefaultLogLevel,
		SessionDir: DefaultSessionPath(),
		HistoryDir: DefaultHistoryPath(DefaultName),
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
	}
}

// DefaultHistoryPath returns the default history directory path for an agent.
// Uses AGENCY_ROOT env var if set, otherwise ~/.agency/history/<name>
func DefaultHistoryPath(name string) string {
	root := os.Getenv("AGENCY_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "/tmp"
		}
		root = filepath.Join(home, ".agency")
	}
	return filepath.Join(root, "history", name)
}

// DefaultSessionPath returns the default session directory path.
// Uses AGENCY_ROOT env var if set, otherwise ~/.agency/sessions
func DefaultSessionPath() string {
	root := os.Getenv("AGENCY_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "/tmp"
		}
		root = filepath.Join(home, ".agency")
	}
	return filepath.Join(root, "sessions")
}
