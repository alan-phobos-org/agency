# agency

A modular framework for AI-powered software engineering agents.

## Overview

Agency provides infrastructure for running AI agents that perform software engineering tasks. The framework separates **agents** (task executors using Claude) from **directors** (work orchestrators) and **views** (dashboards), enabling flexible composition and independent development.

## Components

| Component | Description |
|-----------|-------------|
| **ag-agent-claude** | Executes tasks via Claude CLI in a sandboxed environment |
| **ag-cli** | Command-line tool for task submission, status, and discovery |
| **ag-scheduler** | Runs tasks on cron schedules with configurable jobs |
| **ag-view-web** | Web dashboard with auth, discovery, and task management |

## Quick Start

```bash
# Build all binaries
./build.sh build

# Start the stack (web view + agent)
./deployment/agency.sh

# Access dashboard at https://localhost:8443
```

Requires `AG_WEB_PASSWORD` environment variable (can be set in `.env` file).

## Documentation

- [DEVELOPMENT.md](DEVELOPMENT.md) - Development workflow, testing, and iteration guide
- [AGENTS.md](AGENTS.md) - Project structure and practices (for AI agents and contributors)
- [CHANGELOG.md](CHANGELOG.md) - Release history
- [docs/PLAN.md](docs/PLAN.md) - Vision, phases, and backlog
- [docs/DESIGN.md](docs/DESIGN.md) - Architecture and technical design
- [docs/SCHEDULER_DESIGN.md](docs/SCHEDULER_DESIGN.md) - Scheduler architecture and configuration
- [docs/authentication.md](docs/authentication.md) - Auth system design
- [docs/security-audit.md](docs/security-audit.md) - Security findings

## Development

```bash
# Fast iteration during development
./build.sh quick-test        # Build + deploy + smoke tests (recommended)

# Manual testing steps
./build.sh build             # Build binaries only
./build.sh deploy-local-quick # Deploy locally (fast - skips integration tests)
./build.sh test-smoke        # Run E2E smoke tests

# Code quality
./build.sh check             # Format, lint, and test (run before commits)
./build.sh deploy-local      # Full deployment with all tests
```

See [DEVELOPMENT.md](DEVELOPMENT.md) for detailed workflow guide and [AGENTS.md](AGENTS.md) for project structure.

## License

MIT
