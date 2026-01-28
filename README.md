# agency

A modular framework for AI-powered software engineering agents.

## Overview

Agency provides infrastructure for running AI agents that perform software engineering tasks. The framework separates **agents** (task executors using Claude or Codex) from **directors** (work orchestrators) and **views** (dashboards), enabling flexible composition and independent development.

**Current version: 3.0.3** - Major refactor introducing agency prompts, work queues, and simplified APIs.

## Components

| Component | Description |
|-----------|-------------|
| **ag-agent-claude** | Executes tasks via Claude CLI in a sandboxed environment |
| **ag-agent-codex** | Executes tasks via OpenAI Codex CLI (experimental) |
| **ag-cli** | Command-line tool for task submission, status, and discovery |
| **ag-scheduler** | Runs tasks on cron schedules with configurable jobs |
| **ag-view-web** | Web dashboard with auth, discovery, and task management |

## Key Features

**Agency Prompts (v3.0+)**
- File-based agent instructions loaded from `~/.agency/prompts/`
- Hot-reloadable prompts (no restart needed)
- Mode-based configuration (`prod`/`dev`) via `AGENCY_MODE` env var
- Replaces embedded preprompts for easier customization

**Work Queue**
- FIFO task queuing with 50-task limit and backpressure management
- Automatic dispatch to idle agents with 1-second polling
- File-based persistence across restarts
- Queue visibility in web UI and API

**Security**
- Password authentication for web UI (`AG_WEB_PASSWORD`)
- Self-signed TLS certificates with automatic generation
- Session-based auth with HttpOnly, Secure cookies
- IP-based rate limiting (10 failed attempts = 1 hour block)
- Consolidated TLS handling via `tlsutil` package

**Task Management**
- Multi-turn conversation sessions with automatic resume
- Task history with execution outlines
- Tier-based model selection (fast/standard/heavy)
- Extended thinking always enabled
- Configurable timeouts and environment variables

## Quick Start

```bash
# Build all binaries
./build.sh build

# Start the stack (web view + agent)
./deployment/agency.sh

# Access dashboard at https://localhost:8443
```

**Requirements:**
- `AG_WEB_PASSWORD` environment variable (can be set in `.env` file)
- Agency prompt files in `~/.agency/prompts/` (e.g., `claude-prod.md`)
  - Default prompts are included in `prompts/` directory
  - Set mode with `AGENCY_MODE` env var (`prod` or `dev`, default: `prod`)

## Documentation

**Core Documentation:**
- [AGENTS.md](AGENTS.md) - Quick reference, commands, and workflows (start here)
- [CHANGELOG.md](CHANGELOG.md) - Release history
- [docs/REFERENCE.md](docs/REFERENCE.md) - API specs, endpoints, and configuration

**Architecture & Design:**
- [docs/DESIGN.md](docs/DESIGN.md) - System architecture and technical design
- [docs/PLAN.md](docs/PLAN.md) - Vision, phases, and backlog
- [docs/WEB_UI_DESIGN.md](docs/WEB_UI_DESIGN.md) - Dashboard design and Alpine.js patterns
- [docs/WORK_QUEUE_DESIGN.md](docs/WORK_QUEUE_DESIGN.md) - Task queue architecture
- [docs/SCHEDULER_DESIGN.md](docs/SCHEDULER_DESIGN.md) - Scheduler architecture and configuration
- [docs/authentication.md](docs/authentication.md) - Authentication system design
- [docs/security-audit.md](docs/security-audit.md) - Security findings and audit

**Development:**
- [docs/TESTING.md](docs/TESTING.md) - Test conventions and commands
- [docs/RELEASE.md](docs/RELEASE.md) - Release process
- [docs/DEBUGGING_DEPLOYED.md](docs/DEBUGGING_DEPLOYED.md) - Remote system diagnostics

## Development

**Fast iteration workflow:**
```bash
./build.sh quick-test        # Build + deploy + smoke tests (recommended)
```

**Manual testing:**
```bash
./build.sh build             # Build binaries only
./build.sh deploy-local-quick # Deploy locally (fast - skips integration tests)
./build.sh test-smoke        # Run E2E smoke tests
./build.sh test              # Unit tests only
```

**Pre-commit checks:**
```bash
./build.sh check             # Format, lint, and test (run before commits)
./build.sh deploy-local      # Full deployment with all tests
```

See [AGENTS.md](AGENTS.md) for detailed commands and workflows.

## License

MIT
