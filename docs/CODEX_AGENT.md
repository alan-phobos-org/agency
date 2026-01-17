# Codex Agent Design

This document outlines a Codex-based agent that mirrors the current Claude agent while supporting tiered model selection and agent-kind routing.

## Goals

- Match Claude agent operational behavior (single-task executor, predictable lifecycle, clear errors).
- Run via the Codex CLI in fully permissive mode.
- Use user-facing OAuth mode (subscription-backed), not API keys.
- Support tiered model requests (`fast`, `standard`, `heavy`) mapped per agent.
- Default to Claude for generic tasking, while allowing agent-kind selection.

## Non-Goals

- Replacing the existing Claude agent.
- Introducing multi-task concurrency in a single agent process.
- Building a new scheduler or queue design (reuse existing components).

## Current Claude Agent (Baseline)

The Claude agent (`cmd/ag-agent-claude`) is a single-task HTTP server that:
- Accepts tasks via `/task` and returns `201` with `task_id` and `session_id`.
- Refuses new tasks with `409` when busy.
- Executes the Claude CLI with JSON output and session management flags.
- Stores task history on disk and exposes `/history` endpoints.

This baseline behavior should be preserved for the Codex agent to maintain predictable system behavior.

## Codex Agent Overview

### Binary

Add a new binary: `cmd/ag-agent-codex` that wires the shared agent runtime with a Codex CLI runner.

### CLI Execution

The Codex runner should:
- Resolve the CLI via `CODEX_BIN` or `codex` on PATH.
- Always run in fully permissive mode.
- Use OAuth-based login (no API key mode).
- Produce machine-readable output (JSON if supported).

TODO: Confirm the exact Codex CLI flags for permissive mode, OAuth mode, and JSON output.

### Preprompt

Introduce `internal/agent/codex.md` as the default preprompt, with the same override behavior as Claude (`preprompt_file`).

### Session Handling

Preserve the existing session directory and resume semantics used by the Claude agent:
- New tasks create a fresh `session_id` when omitted.
- Resumed tasks reuse the existing `session_id`.

## Tiered Model Requests

Introduce a generic three-tier selection scheme that maps to provider-specific models.

Tier names:
- `fast`
- `standard`
- `heavy`

### API Changes

- Add `tier` (string enum) to task requests.
- Precedence: `model` (explicit) overrides `tier`; otherwise use `tier`; default to `standard`.
- Return the effective model in task history and status as today.

### Config Changes

Add tier mapping to agent configuration:

```yaml
tiers:
  fast: haiku
  standard: sonnet
  heavy: opus
```

Codex agents will provide their own tier mapping to Codex model names.

## Agent Kind Routing

Introduce a `agent_kind` concept for tasks and discovery.

- `agent_kind` values: `claude`, `codex`.
- Default to `claude` when unspecified.
- Task submission by kind selects the first idle agent of that kind.
- `agent_url` remains an optional override for debugging or explicit routing.

### Status Shape

Include `agent_kind` in the `/status` response so discovery can route appropriately.

## Web + CLI Integration

- Web: add `agent_kind` selection in task submission and queue dispatch.
- CLI: add `--tier` (defaults to `standard`) and `--agent-kind` (defaults to `claude`).

## Scheduler Integration

- Scheduler jobs can specify `tier` (preferred) or `model` (override).
- Default tier is `standard` when neither is provided.

## Compatibility Notes

- Existing clients that send `model` continue to work without changes.
- `tier` is additive and optional.
- Claude remains the default for all generic tasking unless `agent_kind` is specified.

## Open Questions

- TODO: Provide Codex CLI flag details for permissive mode, OAuth mode, and JSON output.
- TODO: Confirm Codex model names for tier mapping.

