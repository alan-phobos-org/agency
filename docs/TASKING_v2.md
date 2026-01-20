# Tasking v2 Design

This document outlines the redesign of the Agency tasking flow, replacing the current "contexts" system with a cleaner "agency prompt" architecture.

---

## Goals

1. **Remove Contexts** - Eliminate the `contexts.yaml` system entirely; replace with file-based "agency prompts" loaded by agents
2. **Remove Preprompt** - Delete embedded `claude.md`/`codex.md` files; agency prompt becomes the single source of all instructions
3. **Agency Prompt Per Agent** - Each agent loads its prompt file from disk at task start, with prod/dev variants
4. **Automatic Prompt Injection** - Web UI, scheduler, and director jobs all get the agency prompt injected at the agent level (invisible to callers)
5. **Simplified Model Selection** - Expose only tiers (`fast`/`standard`/`heavy`), never specific model names; agents interpret tiers internally
6. **Target Agent Kind** - Jobs specify the agent kind (Claude/Codex), defaulting to Claude; model is derived from tier + kind

---

## Current State Analysis

### What Exists Today

| Component | Current Behavior |
|-----------|------------------|
| **Contexts** | `configs/contexts.yaml` defines named presets with `model`, `tier`, `thinking`, `timeout_seconds`, `prompt_prefix` |
| **Context Loading** | `internal/view/web/contexts.go` loads and serves contexts via `/api/contexts` |
| **Context Usage** | Web UI selects context → populates form fields → `prompt_prefix` prepended to user prompt |
| **Preprompt** | Agent loads embedded `claude.md`/`codex.md` or custom file via `preprompt_file` config |
| **Prompt Building** | `agent.buildPrompt()` = `preprompt + project.Prompt + task.Prompt` |
| **Model Selection** | Request can specify `model` (specific) or `tier` (fast/standard/heavy); agent resolves to actual model |
| **Scheduler Jobs** | Jobs specify `model` (opus/sonnet/haiku), `tier`, `agent_kind` |

### Problems with Current Design

1. **Contexts are director-side** - They live in the web view, not the agent; this splits prompt logic across components
2. **Prompt injection is explicit** - Callers must remember to include `prompt_prefix`; easy to bypass
3. **Specific models exposed** - `opus`/`sonnet`/`haiku` are exposed in configs and UIs, coupling to Claude's model names
4. **No dev/prod separation** - No built-in support for different prompts in development vs production

---

## Proposed Design

### 1. Agency Prompt Files

Each agent loads an "agency prompt" from a file at task start. Two variants per agent:

```
~/.agency/prompts/
├── claude-prod.md     # Production agency prompt for Claude agent
├── claude-dev.md      # Development agency prompt for Claude agent
├── codex-prod.md      # Production agency prompt for Codex agent
└── codex-dev.md       # Development agency prompt for Codex agent
```

**Selection logic:**
- Agent detects mode via `AGENCY_MODE` env var (`prod` or `dev`, default: `prod`)
- Loads `<agent_kind>-<mode>.md` from prompts directory
- Falls back to `<agent_kind>-prod.md` if dev variant missing
- Fails task if no prompt file exists (forces proper installation)

**File location:**
- Default: `~/.agency/prompts/`
- Override: `AGENCY_PROMPTS_DIR` env var
- Per-agent override: `agency_prompt_file` in agent config YAML

### 2. Agency Prompt Content

The agency prompt is now the **single source** of all agent instructions. It replaces both the old embedded preprompt (`claude.md`) and the context system.

**Required sections:**

1. **Git Commit Rules** (previously in embedded preprompt)
2. **Code Style** (previously in embedded preprompt)
3. **Environment** (org-specific: GitHub org, auth locations)
4. **Workflow** (org-specific: clone, branch, PR process)

**Production prompt template:**
```markdown
# Agent Instructions

## Git Commits (CRITICAL)
- NEVER mention "Claude", "Anthropic", "AI", "LLM", or "generated" in commit messages
- NEVER add Co-Authored-By headers or "Generated with" footers
- NEVER include emoji in commit messages
- Write commit messages as a human developer would
- Use conventional commit format (feat:, fix:, refactor:, etc.)

## Code Style
- Follow existing project conventions
- Keep changes focused and minimal
- Never add comments mentioning AI, automation, or generation

## Environment
- GitHub org: `alan-phobos-org`
- Auth tokens: `~/.agency/tokens/`

## Workflow
1. Clone the identified repository from the org
2. Reset to HEAD of main: `git checkout main && git reset --hard origin/main`
3. Make changes following existing patterns
4. Commit with clear, descriptive messages
5. Open PR if requested

---
# Task
```

**Dev prompt additionally includes:**
```markdown
## Dev Mode
- Work quickly - this is test/dev work, not production
- Skip thorough testing unless specifically requested
- Don't worry about cleanup
- Prioritize speed over polish
```

**Codex prompts** follow the same structure but may have agent-specific adjustments.

### 3. Prompt Injection at Agent Level

**Key change:** The agency prompt is injected by the agent, not by callers. No more embedded preprompt.

```go
// Current: buildPrompt()
func (a *Agent) buildPrompt(task *Task) string {
    return a.preprompt + "\n\n" + task.Project.Prompt + "\n\n" + task.Prompt
}

// New: buildPrompt()
func (a *Agent) buildPrompt(task *Task) string {
    // Agency prompt loaded fresh each task (allows hot-reload)
    // This is now the ONLY source of instructions - no embedded preprompt
    agencyPrompt := a.loadAgencyPrompt()
    return agencyPrompt + "\n\n" + task.Prompt
}
```

**Benefits:**
- Single source of truth for all instructions
- Callers never see or handle prompts
- Can't accidentally skip the prompt
- Prompt changes don't require rebuilding agent binary
- Hot-reloadable without agent restart
- Full control over instructions at deployment time

### 4. Remove Contexts Entirely

**Files to delete:**
- `configs/contexts.yaml`
- `internal/view/web/contexts.go`
- `internal/view/web/contexts_test.go`

**Code changes:**
- Remove `/api/contexts` endpoint from web director
- Remove context dropdown from web UI (`dashboard.html`)
- Remove `Context` type and `LoadContexts()`
- Remove `PromptPrefix` from `TaskSubmitRequest`
- Remove context-related form fields

**Web UI simplification:**
- User enters prompt directly
- Selects tier (fast/standard/heavy)
- Selects agent kind (Claude/Codex)
- Optionally sets timeout
- No more context selection

### 5. Tier-Only Model Selection

**Remove all specific model references from external interfaces:**

| Current | New |
|---------|-----|
| `model: opus` | `tier: heavy` |
| `model: sonnet` | `tier: standard` |
| `model: haiku` | `tier: fast` |

**Changes:**
- Remove `model` field from `TaskSubmitRequest`
- Remove `model` field from `QueueSubmitRequest`
- Remove `model` from `buildAgentRequest()` helper
- Remove model selector from web UI
- Keep `tier` field (already exists)
- Scheduler jobs use `tier`, not `model`
- Agents internally map tier → model

**Agent tier mapping (internal, not exposed):**

```go
// internal/config/config.go - already exists but becomes authoritative
type TierConfig struct {
    Fast     string `yaml:"fast"`
    Standard string `yaml:"standard"`
    Heavy    string `yaml:"heavy"`
}

func DefaultClaudeTiers() TierConfig {
    return TierConfig{
        Fast:     "haiku",      // Claude specific
        Standard: "sonnet",
        Heavy:    "opus",
    }
}

func DefaultCodexTiers() TierConfig {
    return TierConfig{
        Fast:     "gpt-5.1-codex-mini",
        Standard: "gpt-5.2-codex",
        Heavy:    "gpt-5.1-codex-max",
    }
}
```

### 6. Always Enable Thinking

**Current:** `thinking` is a configurable field (default: true)
**New:** Always enabled, remove the field from APIs

**Changes:**
- Remove `thinking` from `TaskSubmitRequest`
- Remove `thinking` from `QueueSubmitRequest`
- Remove `thinking` from `buildAgentRequest()` helper
- Remove thinking toggle from web UI
- Agent always passes `--thinking` (or equivalent) to CLI

### 7. Agent Kind Selection

**Current:** `agent_kind` exists but isn't prominently exposed
**New:** Make it a primary selector alongside tier

**Web UI changes:**
- Add agent kind selector: "Claude (Recommended)" / "Codex"
- Default to Claude
- Agent kind + tier determines actual model

**Scheduler job changes:**
- `agent_kind` field (already exists)
- `tier` field replaces `model`

**Queue changes:**
- Already has `AgentKind` field
- Remove `Model` field, keep `Tier`

---

## Implementation Changes by File

### Agent Package (`internal/agent/`)

| File | Changes |
|------|---------|
| `agent.go` | Add `loadAgencyPrompt()`, update `buildPrompt()`, remove `preprompt` field, remove `model` from request validation, remove `Project` handling |
| `claude.md` | **DELETE** - instructions move to agency prompt files |
| `codex.md` | **DELETE** - instructions move to agency prompt files |
| `claude_runner.go` | Remove `DefaultPreprompt()` method |
| `codex_runner.go` | Remove `DefaultPreprompt()` method |
| `runner.go` | Remove `DefaultPreprompt()` from `Runner` interface |

### Config Package (`internal/config/`)

| File | Changes |
|------|---------|
| `config.go` | Add `AgencyPromptsDir`, `AgencyMode` fields; remove `PrepromptFile`, remove `ProjectConfig` |

### Web Package (`internal/view/web/`)

| File | Changes |
|------|---------|
| `contexts.go` | **DELETE** |
| `contexts_test.go` | **DELETE** |
| `director.go` | Remove context loading/serving |
| `handlers.go` | Remove `/api/contexts` handler |
| `queue.go` | Remove `Model` from `QueuedTask`, remove `Thinking` |
| `queue_handlers.go` | Update validation (no model), update request types |
| `agent_request.go` | Remove `model` and `thinking` params from `buildAgentRequest()` |
| `dispatcher.go` | Update to not pass model/thinking |
| `dashboard.html` | Remove context dropdown, remove model selector, remove thinking toggle, add agent kind selector |

### Scheduler Package (`internal/scheduler/`)

| File | Changes |
|------|---------|
| `config.go` | Remove `Model` from `Job`, validate tier only |
| `scheduler.go` | Use tier instead of model in requests |

### API Package (`internal/api/`)

| File | Changes |
|------|---------|
| `types.go` | Remove `Model` field validation, remove `ProjectContext` type, keep tier validation |

### Configs (`configs/`)

| File | Changes |
|------|---------|
| `contexts.yaml` | **DELETE** |
| `scheduler.yaml` | Update example jobs to use `tier` not `model` |
| `agent.yaml` | Add `agency_prompts_dir` field |

### Installation

| Action | Details |
|--------|---------|
| Create prompts directory | `~/.agency/prompts/` |
| Create default prompts | `claude-prod.md`, `claude-dev.md`, `codex-prod.md`, `codex-dev.md` |
| Set AGENCY_MODE | Environment variable in deployment scripts |

---

## Migration Path

### Phase 1: Add Agency Prompts (Backwards Compatible)
1. Add `loadAgencyPrompt()` to agent
2. Update `buildPrompt()` to include agency prompt
3. Create default prompt files
4. Test with existing context system still working

### Phase 2: Remove Contexts
1. Delete `contexts.yaml` and `contexts.go`
2. Remove context endpoint and UI
3. Simplify web UI form

### Phase 3: Tier-Only Models
1. Remove `model` from all request types
2. Update scheduler jobs to use `tier`
3. Update web UI to only show tier selector

---

## Design Decisions

The following questions have been resolved:

| Question | Decision |
|----------|----------|
| **Agency prompt file missing** | Fail the task - forces proper installation |
| **Project Context** | Remove entirely - agency prompt covers everything |
| **Embedded preprompt** | Remove entirely - agency prompt is the single source of instructions |
| **Dev mode detection** | `AGENCY_MODE=dev` environment variable |
| **Breaking change timing** | Immediate removal - clean break |
| **Codex model names** | Confirmed correct (gpt-5.1-codex-mini, gpt-5.2-codex, gpt-5.1-codex-max) |
| **Hot-reload** | Per-task reload - allows iteration without restart |

---

## Prompt Architecture

**Before (v1):**
```
embedded preprompt (claude.md)     →  Git rules, code style (compiled into binary)
  +
project context (optional)          →  Per-project instructions
  +
context prompt_prefix (optional)    →  From contexts.yaml
  +
task prompt                         →  User's actual task
```

**After (v2):**
```
agency prompt (file on disk)        →  Everything: git rules, code style, org info, workflow
  +
task prompt                         →  User's actual task
```

**Key changes:**
- Single file contains ALL instructions (git rules + org-specific + workflow)
- No embedded/compiled instructions - full control at deployment time
- No layering complexity - one file per agent/mode combination
- Agency prompt MUST include git commit rules (previously in embedded preprompt)

---

## Summary of Breaking Changes

| Change | Impact | Mitigation |
|--------|--------|------------|
| Remove `contexts.yaml` | Any scripts/tools referencing context IDs | None needed if not used externally |
| Remove `/api/contexts` | Web UI changes, any tools calling endpoint | Remove from UI |
| Remove embedded preprompt | Git rules no longer baked into binary | Agency prompt MUST include git rules section |
| Remove `preprompt_file` config | Any agents using custom preprompt files | Migrate content to agency prompt |
| Remove `model` from requests | Scheduler jobs, external API callers | Update configs to use `tier` |
| Remove `thinking` from requests | Any callers explicitly setting it | None (always-on is usually desired) |
| Remove `Project` from requests | Any callers using project context | Migrate to agency prompt |
| Agency prompt files required | New installation step | Fail task if missing - forces proper setup |

---

## Implementation Status

### Stage 1: Core Code Changes ✅ COMPLETE

Files modified:
- `internal/agent/agent.go` - Added `loadAgencyPrompt()`, updated `buildPrompt()`, removed preprompt field
- `internal/agent/runner.go` - Removed `DefaultPreprompt()` from Runner interface
- `internal/agent/claude.md` - DELETED
- `internal/agent/codex.md` - DELETED
- `internal/config/config.go` - Added `AgencyPromptsDir`, removed `PrepromptFile`, `ProjectConfig`
- `internal/view/web/contexts.go` - DELETED
- `internal/view/web/contexts_test.go` - DELETED
- `internal/view/web/director.go` - Removed contexts loading, `/api/contexts` route, `ContextsPath` from Config
- `internal/view/web/handlers.go` - Removed `HandleContexts` handler
- `internal/view/web/handlers_test.go` - Removed contexts tests, updated `newTestHandlers` signature
- `internal/view/web/queue.go` - Removed `Model`, `Thinking`, `Project` from `QueuedTask` and `QueueSubmitRequest`
- `internal/view/web/queue_handlers.go` - Simplified validation (tier only)
- `internal/view/web/agent_request.go` - Removed `model` and `thinking` params from `buildAgentRequest()`
- `internal/view/web/dispatcher.go` - Updated to not pass model/thinking
- `internal/view/web/templates/dashboard.html` - Removed context dropdown, model selector, thinking toggle
- `internal/view/web/system_test.go` - Removed contexts tests, updated scheduler config to use tier
- `internal/scheduler/config.go` - Removed `Model` from `Job`, added `GetTier()`
- `internal/scheduler/scheduler.go` - Use tier instead of model in requests
- `internal/scheduler/scheduler_test.go` - Updated tests to use tier
- `cmd/ag-view-web/main.go` - Removed `-contexts` flag and `ContextsPath` config
- `configs/contexts.yaml` - DELETED
- `configs/scheduler.yaml` - Updated jobs to use `tier` instead of `model`

### Stage 2: Documentation Updates ✅ COMPLETE

Files updated:
- `docs/DESIGN.md` - Removed `-contexts` flag, updated "Embedded Instructions" to "Agency Prompts"
- `docs/WEB_UI_DESIGN.md` - Removed context picker from task submission, updated options to show tier/timeout
- `docs/REFERENCE.md` - Removed `/api/contexts` endpoint, updated task request fields, removed contexts YAML section
- `AGENTS.md` - Updated configs description, removed contexts reference from web view features
- `CHANGELOG.md` - Added breaking changes section documenting all removals and new agency prompt system
- `deployment/agency.sh` - Removed `-contexts` argument from web view startup
- `deployment/deploy-agency.sh` - Removed contexts.yaml copying and `-contexts` argument
- `build.sh` - Removed contexts.yaml from distribution package

### Stage 3: Verification ✅ COMPLETE

- `internal/view/web/sessions_test.go` - Fixed `NewHandlers` call signature (removed extra nil argument)
- Full test suite passes: `go test ./...` returns all OK
- UI validation: Context dropdown, model selector, and thinking toggle removed from dashboard

---

## Related Documents

- [DESIGN.md](DESIGN.md) - Overall architecture
- [SCHEDULER_DESIGN.md](SCHEDULER_DESIGN.md) - Scheduler details
- [WEB_UI_DESIGN.md](WEB_UI_DESIGN.md) - Web view details
