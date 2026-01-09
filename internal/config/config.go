package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the agent configuration
type Config struct {
	Port          int             `yaml:"port"`
	Name          string          `yaml:"name"` // Agent name (used for history directory)
	LogLevel      string          `yaml:"log_level"`
	SessionDir    string          `yaml:"session_dir"`    // Base directory for session workspaces
	HistoryDir    string          `yaml:"history_dir"`    // Directory for task history storage
	PrepromptFile string          `yaml:"preprompt_file"` // Optional path to custom preprompt file
	Claude        ClaudeConfig    `yaml:"claude"`
	Projects      []ProjectConfig `yaml:"projects,omitempty"`
}

// ProjectConfig defines a project context that can be prepended to task prompts
type ProjectConfig struct {
	Name   string `yaml:"name"`
	Prompt string `yaml:"prompt"`
}

// ClaudeConfig holds Claude CLI settings
type ClaudeConfig struct {
	Model   string        `yaml:"model"`
	Timeout time.Duration `yaml:"timeout"`
}

// Defaults
const (
	DefaultPort       = 9000
	DefaultName       = "agent"
	DefaultModel      = "sonnet"
	DefaultTimeout    = 30 * time.Minute
	DefaultLogLevel   = "info"
	DefaultSessionDir = "/tmp/agency/sessions"
	DefaultHistoryDir = "" // Derived from AGENCY_ROOT or ~/.agency/history/<name>
)

// Parse parses YAML config data
func Parse(data []byte) (*Config, error) {
	cfg := &Config{
		Port:       DefaultPort,
		Name:       DefaultName,
		LogLevel:   DefaultLogLevel,
		SessionDir: DefaultSessionDir,
		Claude: ClaudeConfig{
			Model:   DefaultModel,
			Timeout: DefaultTimeout,
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
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

	validModels := map[string]bool{"opus": true, "sonnet": true, "haiku": true}
	if !validModels[c.Claude.Model] {
		return fmt.Errorf("model must be opus, sonnet, or haiku, got %q", c.Claude.Model)
	}

	if c.Claude.Timeout < time.Second {
		return fmt.Errorf("timeout must be at least 1 second, got %v", c.Claude.Timeout)
	}

	return nil
}

// Default returns a config with default values
func Default() *Config {
	return &Config{
		Port:       DefaultPort,
		Name:       DefaultName,
		LogLevel:   DefaultLogLevel,
		SessionDir: DefaultSessionDir,
		HistoryDir: DefaultHistoryPath(DefaultName),
		Claude: ClaudeConfig{
			Model:   DefaultModel,
			Timeout: DefaultTimeout,
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
