package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadContexts(t *testing.T) {
	t.Parallel()

	t.Run("loads valid contexts file", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "contexts.yaml")
		content := `contexts:
  - id: test-context
    name: Test Context
    description: A test context
    model: opus
    thinking: true
    timeout_seconds: 1800
    prompt_prefix: |
      Test prefix
`
		err := os.WriteFile(configPath, []byte(content), 0644)
		require.NoError(t, err)

		cfg, err := LoadContexts(configPath)
		require.NoError(t, err)
		require.Len(t, cfg.Contexts, 1)

		ctx := cfg.Contexts[0]
		require.Equal(t, "test-context", ctx.ID)
		require.Equal(t, "Test Context", ctx.Name)
		require.Equal(t, "A test context", ctx.Description)
		require.Equal(t, "opus", ctx.Model)
		require.NotNil(t, ctx.Thinking)
		require.True(t, *ctx.Thinking)
		require.Equal(t, 1800, ctx.TimeoutSeconds)
		require.Contains(t, ctx.PromptPrefix, "Test prefix")
	})

	t.Run("loads multiple contexts", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "contexts.yaml")
		content := `contexts:
  - id: ctx1
    name: Context One
  - id: ctx2
    name: Context Two
  - id: ctx3
    name: Context Three
`
		err := os.WriteFile(configPath, []byte(content), 0644)
		require.NoError(t, err)

		cfg, err := LoadContexts(configPath)
		require.NoError(t, err)
		require.Len(t, cfg.Contexts, 3)
		require.Equal(t, "ctx1", cfg.Contexts[0].ID)
		require.Equal(t, "ctx2", cfg.Contexts[1].ID)
		require.Equal(t, "ctx3", cfg.Contexts[2].ID)
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		t.Parallel()

		_, err := LoadContexts("/nonexistent/contexts.yaml")
		require.Error(t, err)
		require.Contains(t, err.Error(), "reading contexts file")
	})

	t.Run("returns error for invalid yaml", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "contexts.yaml")
		err := os.WriteFile(configPath, []byte("invalid: [yaml"), 0644)
		require.NoError(t, err)

		_, err = LoadContexts(configPath)
		require.Error(t, err)
		require.Contains(t, err.Error(), "parsing contexts file")
	})

	t.Run("returns error for missing id", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "contexts.yaml")
		content := `contexts:
  - name: Missing ID
`
		err := os.WriteFile(configPath, []byte(content), 0644)
		require.NoError(t, err)

		_, err = LoadContexts(configPath)
		require.Error(t, err)
		require.Contains(t, err.Error(), "id is required")
	})

	t.Run("returns error for missing name", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "contexts.yaml")
		content := `contexts:
  - id: missing-name
`
		err := os.WriteFile(configPath, []byte(content), 0644)
		require.NoError(t, err)

		_, err = LoadContexts(configPath)
		require.Error(t, err)
		require.Contains(t, err.Error(), "name is required")
	})
}

func TestManualContext(t *testing.T) {
	t.Parallel()

	ctx := ManualContext()
	require.Equal(t, "manual", ctx.ID)
	require.Equal(t, "Manual", ctx.Name)
	require.Equal(t, "Configure settings manually", ctx.Description)
}

func TestContextsConfigGetAllContexts(t *testing.T) {
	t.Parallel()

	t.Run("returns manual plus configured contexts", func(t *testing.T) {
		t.Parallel()

		cfg := &ContextsConfig{
			Contexts: []Context{
				{ID: "ctx1", Name: "Context 1"},
				{ID: "ctx2", Name: "Context 2"},
			},
		}

		all := cfg.GetAllContexts()
		require.Len(t, all, 3)
		require.Equal(t, "manual", all[0].ID)
		require.Equal(t, "ctx1", all[1].ID)
		require.Equal(t, "ctx2", all[2].ID)
	})

	t.Run("returns only manual for nil config", func(t *testing.T) {
		t.Parallel()

		var cfg *ContextsConfig
		all := cfg.GetAllContexts()
		require.Len(t, all, 1)
		require.Equal(t, "manual", all[0].ID)
	})
}

func TestContextsConfigFindContext(t *testing.T) {
	t.Parallel()

	cfg := &ContextsConfig{
		Contexts: []Context{
			{ID: "ctx1", Name: "Context 1"},
			{ID: "ctx2", Name: "Context 2"},
		},
	}

	t.Run("finds manual context", func(t *testing.T) {
		t.Parallel()

		ctx := cfg.FindContext("manual")
		require.NotNil(t, ctx)
		require.Equal(t, "manual", ctx.ID)
	})

	t.Run("finds configured context", func(t *testing.T) {
		t.Parallel()

		ctx := cfg.FindContext("ctx1")
		require.NotNil(t, ctx)
		require.Equal(t, "ctx1", ctx.ID)
		require.Equal(t, "Context 1", ctx.Name)
	})

	t.Run("returns nil for unknown context", func(t *testing.T) {
		t.Parallel()

		ctx := cfg.FindContext("unknown")
		require.Nil(t, ctx)
	})

	t.Run("returns nil for nil config", func(t *testing.T) {
		t.Parallel()

		var nilCfg *ContextsConfig
		ctx := nilCfg.FindContext("ctx1")
		require.Nil(t, ctx)
	})
}

func TestThinkingPointerSerialization(t *testing.T) {
	t.Parallel()

	t.Run("thinking true", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "contexts.yaml")
		content := `contexts:
  - id: test
    name: Test
    thinking: true
`
		err := os.WriteFile(configPath, []byte(content), 0644)
		require.NoError(t, err)

		cfg, err := LoadContexts(configPath)
		require.NoError(t, err)
		require.NotNil(t, cfg.Contexts[0].Thinking)
		require.True(t, *cfg.Contexts[0].Thinking)
	})

	t.Run("thinking false", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "contexts.yaml")
		content := `contexts:
  - id: test
    name: Test
    thinking: false
`
		err := os.WriteFile(configPath, []byte(content), 0644)
		require.NoError(t, err)

		cfg, err := LoadContexts(configPath)
		require.NoError(t, err)
		require.NotNil(t, cfg.Contexts[0].Thinking)
		require.False(t, *cfg.Contexts[0].Thinking)
	})

	t.Run("thinking not specified", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "contexts.yaml")
		content := `contexts:
  - id: test
    name: Test
`
		err := os.WriteFile(configPath, []byte(content), 0644)
		require.NoError(t, err)

		cfg, err := LoadContexts(configPath)
		require.NoError(t, err)
		require.Nil(t, cfg.Contexts[0].Thinking)
	})
}
