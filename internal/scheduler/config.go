package scheduler

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
	"phobos.org.uk/agency/internal/api"
)

// Config represents the scheduler configuration
type Config struct {
	Port        int    `yaml:"port"`
	LogLevel    string `yaml:"log_level"`
	DirectorURL string `yaml:"director_url"` // Primary target for session tracking (optional)
	AgentURL    string `yaml:"agent_url"`    // Fallback if director unavailable
	AgentKind   string `yaml:"agent_kind"`   // Default agent kind for jobs
	Jobs        []Job  `yaml:"jobs"`
}

// Job represents a scheduled job
type Job struct {
	Name      string        `yaml:"name"`
	Schedule  string        `yaml:"schedule"`
	Prompt    string        `yaml:"prompt"`
	Model     string        `yaml:"model,omitempty"`
	Tier      string        `yaml:"tier,omitempty"`
	Timeout   time.Duration `yaml:"timeout,omitempty"`
	AgentURL  string        `yaml:"agent_url,omitempty"`
	AgentKind string        `yaml:"agent_kind,omitempty"`
}

// Defaults
const (
	DefaultPort      = 9100
	DefaultLogLevel  = "info"
	DefaultAgentURL  = "https://localhost:9000"
	DefaultModel     = "sonnet"
	DefaultTimeout   = 30 * time.Minute
	DefaultAgentKind = api.AgentKindClaude
)

// Parse parses YAML config data
func Parse(data []byte) (*Config, error) {
	cfg := &Config{
		Port:      DefaultPort,
		LogLevel:  DefaultLogLevel,
		AgentURL:  DefaultAgentURL,
		AgentKind: DefaultAgentKind,
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
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
	if c.AgentKind != "" && c.AgentKind != api.AgentKindClaude && c.AgentKind != api.AgentKindCodex {
		return fmt.Errorf("agent_kind must be claude or codex, got %q", c.AgentKind)
	}

	if len(c.Jobs) == 0 {
		return fmt.Errorf("at least one job is required")
	}

	seenNames := make(map[string]bool)
	for i, job := range c.Jobs {
		if job.Name == "" {
			return fmt.Errorf("job[%d]: name is required", i)
		}
		if seenNames[job.Name] {
			return fmt.Errorf("job[%d]: duplicate name %q", i, job.Name)
		}
		seenNames[job.Name] = true

		if job.Schedule == "" {
			return fmt.Errorf("job[%d] %q: schedule is required", i, job.Name)
		}
		if _, err := ParseCron(job.Schedule); err != nil {
			return fmt.Errorf("job[%d] %q: invalid schedule: %w", i, job.Name, err)
		}

		if job.Prompt == "" {
			return fmt.Errorf("job[%d] %q: prompt is required", i, job.Name)
		}

		jobKind := c.GetAgentKind(&job)
		if jobKind != api.AgentKindClaude && jobKind != api.AgentKindCodex {
			return fmt.Errorf("job[%d] %q: agent_kind must be claude or codex, got %q", i, job.Name, jobKind)
		}

		if job.Tier != "" && !api.IsValidTier(job.Tier) {
			return fmt.Errorf("job[%d] %q: tier must be fast, standard, or heavy, got %q", i, job.Name, job.Tier)
		}

		if job.Model != "" && jobKind == api.AgentKindClaude {
			validModels := map[string]bool{"opus": true, "sonnet": true, "haiku": true}
			if !validModels[job.Model] {
				return fmt.Errorf("job[%d] %q: model must be opus, sonnet, or haiku, got %q", i, job.Name, job.Model)
			}
		}
	}

	return nil
}

// GetAgentURL returns the agent URL for a job, using the global default if not specified
func (c *Config) GetAgentURL(job *Job) string {
	if job.AgentURL != "" {
		return job.AgentURL
	}
	return c.AgentURL
}

// GetAgentKind returns the agent kind for a job, using defaults if not specified.
func (c *Config) GetAgentKind(job *Job) string {
	if job.AgentKind != "" {
		return job.AgentKind
	}
	if c.AgentKind != "" {
		return c.AgentKind
	}
	return api.AgentKindClaude
}

// GetModel returns the model override for a job.
func (c *Config) GetModel(job *Job) string {
	return job.Model
}

// GetTimeout returns the timeout for a job, using the default if not specified
func (c *Config) GetTimeout(job *Job) time.Duration {
	if job.Timeout > 0 {
		return job.Timeout
	}
	return DefaultTimeout
}
