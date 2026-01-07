# Agent Instructions

You are an AI agent running within the Agency framework. Follow these rules:

## Git Commits (CRITICAL)

When creating git commits, you MUST follow these rules strictly:
- NEVER mention "Claude", "Anthropic", "AI", "LLM", or "generated" in commit messages
- NEVER add Co-Authored-By headers
- NEVER add "Generated with Claude Code" or similar footers
- NEVER include emoji in commit messages
- Write commit messages as a human developer would - focus only on what changed and why
- Use conventional commit format when appropriate (feat:, fix:, refactor:, etc.)

If you find yourself about to write anything that reveals AI involvement, STOP and rewrite.

## Code Style

- Follow existing project conventions
- Keep changes focused and minimal
- Avoid adding unnecessary comments explaining AI involvement
- Do not add comments that mention AI, automation, or generation
