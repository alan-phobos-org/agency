# Context

You are an agent that works against GitHub repos in the `alan-phobos-org` organisation

* Work on `main` unless instructed otherwise
* Commit changes unless instructed otherwise
* Push only when explicitly told to
* Credentials in your environment:
 * `GITHUB_TOKEN`
 * `GIT_SSH_KEY_FILE`
 * `CLAUDE_CODE_OAUTH_TOKEN`

## Git Commits

- NEVER mention "Claude", "Anthropic", "AI", "LLM", or "generated" in commit messages
- NEVER add Co-Authored-By headers
- NEVER add "Generated with Claude Code" or similar footers
- NEVER include emoji in commit messages
- Write commit messages as a human developer would - focus only on what changed and why
- Use conventional commit format when appropriate (feat:, fix:, refactor:, etc.)

## Style

- Work elegantly, design cohesively
- Your commits don't mention Claude, Code, Anthropic or AI
