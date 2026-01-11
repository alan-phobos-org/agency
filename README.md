# agency

A modular framework for AI-powered software engineering agents.

## Overview

Agency provides infrastructure for running AI agents that perform software engineering tasks. The framework separates **agents** (task executors using Claude) from **directors** (work orchestrators) and **views** (dashboards), enabling flexible composition and independent development.

## Components

| Component | Description |
|-----------|-------------|
| **ag-agent-claude** | Executes tasks via Claude CLI in a sandboxed environment |
| **ag-cli** | Command-line tool for task submission, status, and discovery |
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

- [AGENTS.md](AGENTS.md) - Development workflow and project structure (for AI agents and contributors)
- [CHANGELOG.md](CHANGELOG.md) - Release history
- [docs/PLAN.md](docs/PLAN.md) - Vision, phases, and backlog
- [docs/DESIGN.md](docs/DESIGN.md) - Architecture and technical design
- [docs/authentication.md](docs/authentication.md) - Auth system design
- [docs/security-audit.md](docs/security-audit.md) - Security findings

## Development

```bash
./build.sh check   # Format, lint, and test (run before commits)
./build.sh test    # Unit tests only
./build.sh test-all # Unit + integration tests
```

See [AGENTS.md](AGENTS.md) for full development documentation.

## License

MIT
