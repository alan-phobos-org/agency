# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- Fixed prompt construction duplicating preprompt when project context is present

## [2.2.0] - 2026-01-17

### Added
- **Work Queue**: Task queuing system for handling multiple concurrent requests
  - FIFO queue with 50-task limit for backpressure management
  - Automatic task dispatch to idle agents via background dispatcher (1s polling)
  - Queue API endpoints for task submission, status, and cancellation
  - File-based persistence for queue state across restarts
  - Web UI integration for queue visibility and management
  - Support for all task submitters (Web UI, Scheduler, CLI)
- Documentation: Work Queue Design document with architecture details
- Documentation: Claude Code CLI authentication reference

### Changed
- Enhanced web UI with improved dashboard and queue management interface
- Improved Playwright smoke test robustness with better error handling

### Fixed
- Fixed potential panic in `/status` endpoint when task is queued but not yet started
- Fixed scheduler UI bugs in job display and status updates

### Infrastructure
- CI: Use CLAUDE_CODE_OAUTH_TOKEN for smoke test authentication
- CI: Upload logs and artifacts on smoke test failures for debugging
- CI: Add Claude Code CLI installation step for smoke tests
- Testing: Add smoke test screenshots for better documentation

## [1.1.0] - 2026-01-13

### Added
- **ag-scheduler**: New scheduler component for running tasks on cron schedules
  - Cron-style scheduling with standard 5-field expressions
  - Configurable agent URL, model, and timeout per job
  - Status endpoint showing job states and next run times
  - Fire-and-forget task submission (no completion tracking)
  - Sample config for nightly maintenance across repositories

### Documentation
- Added docs/SCHEDULER_DESIGN.md with architecture and configuration reference

## [1.0.1] - 2026-01-11

### Fixed
- Remote deployment script (`deploy-agency.sh`) now correctly loads `AG_WEB_PASSWORD` instead of the old `AG_WEB_TOKEN` variable
- Added error handling to env var loading to prevent script failures when optional variables are missing

## [1.0.0] - 2026-01-10

### Added

#### Core Components
- **ag-agent-claude**: Agent binary that wraps Claude CLI for task execution
  - REST API for task submission, status, and cancellation
  - Session support for multi-turn conversations
  - Automatic resume on max turns limit
  - Embedded CLAUDE.md instructions for consistent behavior
  - Task history with execution outlines

- **ag-cli**: Command-line tool for interacting with agents
  - `task` command to submit tasks and poll until completion
  - `status` command to check component status
  - `discover` command to find running components

- **ag-view-web**: Web-based dashboard for monitoring and control
  - HTTPS with auto-generated self-signed certificates
  - Password-based authentication with secure session cookies
  - Device pairing for multi-device access
  - IP rate limiting for auth protection
  - Port scanning discovery of agents and directors
  - Real-time status updates with 1-second polling
  - Task submission with model/timeout/thinking selection
  - Task contexts for predefined prompt settings
  - Global sessions shared across UI views
  - Session detail view with task history

#### Features
- Interface-based architecture (Statusable, Taskable, Observable, Configurable)
- Extended thinking support (auto-enabled by Claude CLI for compatible models)
- Project context support for task prompts
- Custom preprompt loading from file
- Configurable history retention (100 outlines, 20 debug logs)
- Local deployment scripts for quick setup

### Security
- Password authentication required for web view (AG_WEB_PASSWORD)
- HttpOnly, Secure, SameSite=Strict session cookies
- Rate limiting: 10 failed attempts = 1 hour block
- Bearer token and query param auth for API access
- Optional access logging

### Infrastructure
- Build system with format, lint, and test targets
- Unit, integration, component, and system tests
- Race condition detection in tests

[Unreleased]: https://github.com/alan-phobos-org/agency/compare/v2.2.0...HEAD
[2.2.0]: https://github.com/alan-phobos-org/agency/compare/v2.1.0...v2.2.0
[1.1.0]: https://github.com/alan-phobos-org/agency/compare/v1.0.1...v1.1.0
[1.0.1]: https://github.com/alan-phobos-org/agency/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/alan-phobos-org/agency/releases/tag/v1.0.0
