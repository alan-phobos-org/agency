# Testing Guide

Testing conventions and commands for Agency.

**Related:** [AGENTS.md](../AGENTS.md) (quick reference), [DESIGN.md](DESIGN.md) (architecture)

---

## Conventions

- Tests use `t.Parallel()` unless sharing state
- Use `testutil.AllocateTestPort(t)` for unique ports
- Mock Claude CLI via `CLAUDE_BIN` env var pointing to `testdata/mock-claude`
- Print progress to stderr, not t.Log()
- Use production-style IDs in tests (e.g., `task-abc123` not `test123`)
- Create fresh `NewHandlers()` instance per test
- Use `httptest.NewRequest` and `httptest.NewRecorder` for handler tests

---

## Test Levels

| Level | Speed | Claude | Purpose |
|-------|-------|--------|---------|
| Unit | Fast | Mock | Individual functions |
| Integration | Fast | Mock | Component interactions |
| System | Fast | Mock | Binary execution, API contracts |
| Smoke | Slow | Real (haiku) | Full E2E with Playwright |

---

## Race Condition Prevention

Tests run with `-race` flag in CI. When using `httptest.NewServer` with shared variables:

```go
// WRONG - race condition between handler goroutine and test goroutine
var count int
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    count++  // Write from handler goroutine
}))
assert.Equal(t, 1, count)  // Read from test goroutine - RACE!

// CORRECT - use sync/atomic for cross-goroutine access
var count int32
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    atomic.AddInt32(&count, 1)  // Atomic write
}))
assert.Equal(t, int32(1), atomic.LoadInt32(&count))  // Atomic read
```

**Rule**: Any variable accessed from both an HTTP handler and the test function MUST use `sync/atomic` or a mutex.

---

## Commands

| Command | Purpose | When to Use |
|---------|---------|-------------|
| `./build.sh test` | Unit tests (<5s) | Quick validation |
| `./build.sh test-all` | Unit + integration | Before PR |
| `./build.sh test-int` | Integration only | Testing API changes |
| `./build.sh test-sys` | System tests | End-to-end validation |
| `./build.sh test-release` | Full suite | Before release |

---

## Coverage by Package

| Package | Tests |
|---------|-------|
| internal/agent | Unit + Integration + System |
| internal/config | Unit (validation) |
| internal/history | Unit (storage, pruning) |
| internal/scheduler | Unit (cron, config, job submission) |
| internal/view/web | Unit + Integration + System |
| cmd/* | None (thin entry points) |
