package web

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Context represents a predefined task context with settings
type Context struct {
	ID             string `yaml:"id" json:"id"`
	Name           string `yaml:"name" json:"name"`
	Description    string `yaml:"description,omitempty" json:"description,omitempty"`
	Model          string `yaml:"model,omitempty" json:"model,omitempty"`
	Thinking       *bool  `yaml:"thinking,omitempty" json:"thinking,omitempty"`
	TimeoutSeconds int    `yaml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`
	PromptPrefix   string `yaml:"prompt_prefix,omitempty" json:"prompt_prefix,omitempty"`
}

// ContextsConfig holds all context definitions
type ContextsConfig struct {
	Contexts []Context `yaml:"contexts" json:"contexts"`
}

// LoadContexts loads contexts from a YAML file
func LoadContexts(path string) (*ContextsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading contexts file: %w", err)
	}

	var cfg ContextsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing contexts file: %w", err)
	}

	// Validate contexts
	for i, ctx := range cfg.Contexts {
		if ctx.ID == "" {
			return nil, fmt.Errorf("context %d: id is required", i)
		}
		if ctx.Name == "" {
			return nil, fmt.Errorf("context %s: name is required", ctx.ID)
		}
	}

	return &cfg, nil
}

// ManualContext returns the special "manual" context that allows user customization
func ManualContext() Context {
	return Context{
		ID:          "manual",
		Name:        "Manual",
		Description: "Configure settings manually",
	}
}

// GetAllContexts returns all contexts including the manual option
func (c *ContextsConfig) GetAllContexts() []Context {
	result := []Context{ManualContext()}
	if c != nil {
		result = append(result, c.Contexts...)
	}
	return result
}

// FindContext finds a context by ID
func (c *ContextsConfig) FindContext(id string) *Context {
	if id == "manual" {
		ctx := ManualContext()
		return &ctx
	}
	if c == nil {
		return nil
	}
	for _, ctx := range c.Contexts {
		if ctx.ID == id {
			return &ctx
		}
	}
	return nil
}
