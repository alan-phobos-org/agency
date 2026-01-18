# Streaming Task Logs Enhancement

## Overview

Currently, the agent logs only high-level task lifecycle events (created, started, completed). This document describes how to enhance observability by capturing and logging Claude CLI's streaming events in real-time, with **detailed tool-specific context** that provides meaningful insight into what the agent is doing.

## Current State

The agent captures these events:
- `task created` - When task is queued
- `task started` - When execution begins
- `task completed/failed` - When execution ends

This provides minimal visibility into what happens during task execution.

## Design Goals

1. **Glanceable Progress** - Operators should see meaningful context at a glance without reading raw JSON
2. **Tool-Specific Detail** - Each tool type gets tailored logging showing the most relevant fields
3. **Output Previews** - Show first 32 characters of results + return codes where applicable
4. **Streamable** - Events logged in real-time as they arrive from Claude CLI

## Proposed Enhancement

### Claude CLI Stream JSON Format

Using `--output-format stream-json --verbose`, Claude CLI emits structured JSON events:

```json
// Session initialization
{"type":"system","subtype":"init","session_id":"...","tools":[...],"model":"..."}

// Assistant response (may include tool calls)
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01X...","name":"Bash","input":{"command":"git status"}}]}}

// Tool execution result
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01X...","content":"...","is_error":false}]}}

// Final result
{"type":"result","subtype":"success","duration_ms":...,"num_turns":...,"total_cost_usd":...}
```

#### Tool Result Content Structure

Per the [Anthropic API Messages documentation](https://platform.claude.com/docs/en/api/messages):

- **`content`**: Can be either a **string** or an **array of content blocks** (text, image, etc.)
- **`is_error`**: Optional boolean indicating if the tool invocation failed
- **`tool_use_id`**: Correlates with the `id` field from the corresponding `tool_use` block

When `is_error: true`, the content contains an error message describing the failure. This allows us to detect tool failures without parsing content heuristically.

### Tool-Specific Logging Specification

Each tool call is logged at INFO level with tool-specific context extracted from the input.
Tool results are logged at DEBUG level with output preview and status.

#### Bash Tool

**On tool_use:**
```
INFO  bash command      [cmd="git status --short"]
```

**On tool_result:**
```
DEBUG bash result       [exit=0, out="M  internal/agent/agent.go..."]
```

Fields:
- `cmd`: The command being executed (truncated to 64 chars if longer)
- `exit`: Exit code from the command (0 = success)
- `out`: First 32 characters of stdout, with "..." if truncated

#### Read Tool

**On tool_use:**
```
INFO  read file         [path="internal/agent/agent.go", lines=1-100]
```

**On tool_result:**
```
DEBUG read result       [bytes=4523]
```

Fields:
- `path`: Relative file path (stripped of common prefixes)
- `lines`: Line range if offset/limit specified
- `bytes`: Size of content returned

#### Write Tool

**On tool_use:**
```
INFO  write file        [path="internal/agent/stream.go", bytes=1234]
```

**On tool_result:**
```
DEBUG write result      [ok=true]
```

Fields:
- `path`: Relative file path
- `bytes`: Size of content being written
- `ok`: Whether write succeeded

#### Edit Tool

**On tool_use:**
```
INFO  edit file         [path="agent.go", old="func foo()...", new="func bar()..."]
```

**On tool_result:**
```
DEBUG edit result       [ok=true]
```

Fields:
- `path`: File being edited
- `old`: First 24 chars of old_string
- `new`: First 24 chars of new_string
- `ok`: Whether edit succeeded

#### Glob Tool

**On tool_use:**
```
INFO  glob search       [pattern="**/*.go", path="/repo"]
```

**On tool_result:**
```
DEBUG glob result       [matches=15]
```

Fields:
- `pattern`: The glob pattern
- `path`: Search root directory
- `matches`: Number of files matched

#### Grep Tool

**On tool_use:**
```
INFO  grep search       [pattern="func Execute", type="go"]
```

**On tool_result:**
```
DEBUG grep result       [matches=3]
```

Fields:
- `pattern`: The search regex
- `type`: File type filter (if specified)
- `path`: Search path (if specified)
- `matches`: Number of matches found

#### WebSearch Tool

**On tool_use:**
```
INFO  web search        [query="golang streaming json parser"]
```

**On tool_result:**
```
DEBUG search result     [results=5]
```

Fields:
- `query`: The search query
- `results`: Number of results returned

#### WebFetch Tool

**On tool_use:**
```
INFO  web fetch         [url="https://docs.github.com/..."]
```

**On tool_result:**
```
DEBUG fetch result      [status=200, bytes=15234]
```

Fields:
- `url`: The URL being fetched (truncated to 64 chars)
- `status`: HTTP status code
- `bytes`: Size of response body

#### Task Tool (Subagent)

**On tool_use:**
```
INFO  spawn agent       [type="Explore", desc="find auth handlers"]
```

**On tool_result:**
```
DEBUG agent result      [ok=true, chars=1523]
```

Fields:
- `type`: Subagent type (Explore, Bash, Plan, etc.)
- `desc`: Task description
- `ok`: Whether subagent succeeded
- `chars`: Length of subagent response

#### TodoWrite Tool

**On tool_use:**
```
INFO  update todos      [count=3, pending=2, done=1]
```

Fields:
- `count`: Total number of todos
- `pending`: Number in pending state
- `done`: Number completed

#### AskUserQuestion Tool

**On tool_use:**
```
INFO  ask user          [questions=1, header="Auth method"]
```

Fields:
- `questions`: Number of questions being asked
- `header`: First question's header

#### Other/Unknown Tools

**On tool_use:**
```
INFO  tool call         [tool="SomeTool", input_bytes=234]
```

**On tool_result:**
```
DEBUG tool result       [tool="SomeTool", output_bytes=567]
```

Generic fallback for any unrecognized tool.

### Summary Event Types

| Event Type | Log Level | Description |
|------------|-----------|-------------|
| `system/init` | debug | Session initialized with model and tools |
| `assistant` with `tool_use` | info | Tool-specific formatted log (see above) |
| `user` with `tool_result` | debug | Tool-specific result with preview |
| `assistant` with `text` | debug | Text response (length only, no content) |
| `result` | info | Final metrics: duration, turns, cost |

### Parallel Tool Calls

Claude can invoke multiple tools in a single assistant message. Each tool call is logged as a **separate log entry** in the order they appear in the message. This provides:
- Clear visibility into each tool being invoked
- Easier log filtering by tool name
- Natural correlation with results (which also arrive separately)

Example with parallel Read and Grep:
```
15:05:57 INFO  read file        [path="internal/agent/agent.go"]
15:05:57 INFO  grep search      [pattern="executeTask"]
15:05:58 DEBUG read result      [bytes=24531]
15:05:58 DEBUG grep result      [matches=3]
```

### Implementation Changes

#### 1. Agent Configuration

Add flag to enable streaming logs:

```go
type Config struct {
    // ...existing fields...
    StreamLogs bool `env:"AG_STREAM_LOGS" default:"true"`
}
```

#### 2. Modify executeTask()

Change from buffered stdout capture to streaming line reader:

```go
func (a *Agent) executeTask(task *Task, env map[string]string) {
    // Add --output-format stream-json --verbose to args
    args := a.buildClaudeArgs(task)
    args = append(args, "--output-format", "stream-json", "--verbose")

    cmd := exec.CommandContext(ctx, claudeBin, args...)
    stdout, _ := cmd.StdoutPipe()

    // Start command
    cmd.Start()

    // Stream and log events
    scanner := bufio.NewScanner(stdout)
    var lastResult StreamEvent
    for scanner.Scan() {
        line := scanner.Bytes()
        event := parseStreamEvent(line)
        a.logStreamEvent(task.ID, event)
        if event.Type == "result" {
            lastResult = event
        }
    }

    cmd.Wait()
    // Use lastResult for task completion
}
```

#### 3. Stream Event Parser

```go
type StreamEvent struct {
    Type    string `json:"type"`    // system, assistant, user, result
    Subtype string `json:"subtype"` // init, success, error
    Message struct {
        Content []ContentBlock `json:"content"`
    } `json:"message"`
    DurationMS    int     `json:"duration_ms"`
    NumTurns      int     `json:"num_turns"`
    TotalCostUSD  float64 `json:"total_cost_usd"`
}

type ContentBlock struct {
    Type      string          `json:"type"`        // text, tool_use, tool_result
    ID        string          `json:"id"`          // tool_use ID for correlation
    Name      string          `json:"name"`        // tool name
    Text      string          `json:"text"`        // text content
    Input     json.RawMessage `json:"input"`       // tool input (raw JSON)
    ToolUseID string          `json:"tool_use_id"` // for tool_result
    Content   json.RawMessage `json:"content"`     // tool_result content (string or array)
    IsError   bool            `json:"is_error"`    // true if tool invocation failed
}

// ToolCallTracker correlates tool_use with tool_result for detailed logging
type ToolCallTracker struct {
    pendingCalls map[string]ContentBlock // ID -> tool_use block
}

func (a *Agent) logStreamEvent(taskID string, event StreamEvent, tracker *ToolCallTracker) {
    taskLog := a.log.WithTask(taskID)

    switch event.Type {
    case "system":
        if event.Subtype == "init" {
            taskLog.Debug("session initialized", nil)
        }

    case "assistant":
        for _, block := range event.Message.Content {
            switch block.Type {
            case "tool_use":
                // Track for later correlation with result
                tracker.pendingCalls[block.ID] = block
                // Log tool-specific message
                a.logToolCall(taskLog, block)
            case "text":
                if len(block.Text) > 0 {
                    taskLog.Debug("assistant response", map[string]any{
                        "length": len(block.Text),
                    })
                }
            }
        }

    case "user":
        for _, block := range event.Message.Content {
            if block.Type == "tool_result" {
                // Find the original tool_use for context
                if toolUse, ok := tracker.pendingCalls[block.ToolUseID]; ok {
                    a.logToolResult(taskLog, toolUse, block)
                    delete(tracker.pendingCalls, block.ToolUseID)
                }
            }
        }

    case "result":
        taskLog.Info("execution complete", map[string]any{
            "duration_ms": event.DurationMS,
            "turns":       event.NumTurns,
            "cost_usd":    event.TotalCostUSD,
        })
    }
}
```

#### 4. Tool-Specific Logging Functions

```go
func (a *Agent) logToolCall(taskLog *TaskLogger, block ContentBlock) {
    var input map[string]any
    json.Unmarshal(block.Input, &input)

    switch block.Name {
    case "Bash":
        cmd := truncate(getString(input, "command"), 64)
        taskLog.Info("bash command", map[string]any{"cmd": cmd})

    case "Read":
        fields := map[string]any{"path": getString(input, "file_path")}
        if offset := getInt(input, "offset"); offset > 0 {
            limit := getInt(input, "limit")
            fields["lines"] = fmt.Sprintf("%d-%d", offset, offset+limit)
        }
        taskLog.Info("read file", fields)

    case "Write":
        taskLog.Info("write file", map[string]any{
            "path":  getString(input, "file_path"),
            "bytes": len(getString(input, "content")),
        })

    case "Edit":
        taskLog.Info("edit file", map[string]any{
            "path": filepath.Base(getString(input, "file_path")),
            "old":  truncate(getString(input, "old_string"), 24),
            "new":  truncate(getString(input, "new_string"), 24),
        })

    case "Glob":
        taskLog.Info("glob search", map[string]any{
            "pattern": getString(input, "pattern"),
            "path":    getString(input, "path"),
        })

    case "Grep":
        fields := map[string]any{"pattern": getString(input, "pattern")}
        if t := getString(input, "type"); t != "" {
            fields["type"] = t
        }
        taskLog.Info("grep search", fields)

    case "WebSearch":
        taskLog.Info("web search", map[string]any{
            "query": getString(input, "query"),
        })

    case "WebFetch":
        taskLog.Info("web fetch", map[string]any{
            "url": truncate(getString(input, "url"), 64),
        })

    case "Task":
        taskLog.Info("spawn agent", map[string]any{
            "type": getString(input, "subagent_type"),
            "desc": truncate(getString(input, "description"), 32),
        })

    case "TodoWrite":
        todos := getArray(input, "todos")
        pending, done := 0, 0
        for _, t := range todos {
            if m, ok := t.(map[string]any); ok {
                if m["status"] == "pending" { pending++ }
                if m["status"] == "completed" { done++ }
            }
        }
        taskLog.Info("update todos", map[string]any{
            "count": len(todos), "pending": pending, "done": done,
        })

    case "AskUserQuestion":
        questions := getArray(input, "questions")
        fields := map[string]any{"questions": len(questions)}
        if len(questions) > 0 {
            if q, ok := questions[0].(map[string]any); ok {
                fields["header"] = getString(q, "header")
            }
        }
        taskLog.Info("ask user", fields)

    default:
        taskLog.Info("tool call", map[string]any{
            "tool": block.Name,
            "input_bytes": len(block.Input),
        })
    }
}

func (a *Agent) logToolResult(taskLog *TaskLogger, toolUse, result ContentBlock) {
    content := extractContent(result.Content) // handles string or array
    isError := result.IsError

    switch toolUse.Name {
    case "Bash":
        exit := parseExitCode(content)
        if isError { exit = 1 } // Ensure non-zero on error
        taskLog.Debug("bash result", map[string]any{
            "exit": exit,
            "out":  truncate(content, 32),
        })

    case "Read":
        fields := map[string]any{"bytes": len(content)}
        if isError { fields["error"] = true }
        taskLog.Debug("read result", fields)

    case "Write", "Edit":
        taskLog.Debug(strings.ToLower(toolUse.Name)+" result", map[string]any{
            "ok": !isError,
        })

    case "Glob":
        matches := strings.Count(content, "\n")
        if isError { matches = 0 }
        taskLog.Debug("glob result", map[string]any{"matches": matches})

    case "Grep":
        matches := strings.Count(content, "\n")
        if isError { matches = 0 }
        taskLog.Debug("grep result", map[string]any{"matches": matches})

    case "WebSearch":
        results := strings.Count(content, "](http")
        if isError { results = 0 }
        taskLog.Debug("search result", map[string]any{"results": results})

    case "WebFetch":
        fields := map[string]any{"bytes": len(content)}
        if isError { fields["error"] = true }
        taskLog.Debug("fetch result", fields)

    case "Task":
        taskLog.Debug("agent result", map[string]any{
            "ok": !isError, "chars": len(content),
        })

    default:
        fields := map[string]any{"tool": toolUse.Name, "output_bytes": len(content)}
        if isError { fields["error"] = true }
        taskLog.Debug("tool result", fields)
    }
}

// extractContent normalizes content which can be string or array of blocks
func extractContent(raw json.RawMessage) string {
    var str string
    if json.Unmarshal(raw, &str) == nil {
        return str
    }
    // Array of content blocks - extract text
    var blocks []struct {
        Type string `json:"type"`
        Text string `json:"text"`
    }
    if json.Unmarshal(raw, &blocks) == nil {
        var sb strings.Builder
        for _, b := range blocks {
            if b.Type == "text" {
                sb.WriteString(b.Text)
            }
        }
        return sb.String()
    }
    return ""
}

// Helper functions
func truncate(s string, max int) string {
    if len(s) <= max { return s }
    return s[:max] + "..."
}

func getString(m map[string]any, key string) string {
    if v, ok := m[key].(string); ok { return v }
    return ""
}

func getInt(m map[string]any, key string) int {
    if v, ok := m[key].(float64); ok { return int(v) }
    return 0
}

func getArray(m map[string]any, key string) []any {
    if v, ok := m[key].([]any); ok { return v }
    return nil
}

func parseExitCode(content string) int {
    // Look for exit code in error messages or tool output
    if strings.Contains(content, "Exit code:") {
        // Parse "Exit code: N" format
    }
    return 0 // Default to success
}
```

### Example Log Output

With this enhancement, a task executing "fix the build error in agent.go" might show:

```
15:05:56 INFO  task created     [model=sonnet, session_id=abc123]
15:05:56 INFO  task started     [timeout_seconds=1800]
15:05:56 DEBUG session init     []
15:05:57 INFO  read file        [path="internal/agent/agent.go"]
15:05:57 DEBUG read result      [bytes=24531]
15:05:58 INFO  bash command     [cmd="go build ./..."]
15:05:59 DEBUG bash result      [exit=1, out="internal/agent/agent.go:..."]
15:06:00 INFO  grep search      [pattern="undefined: foo", type="go"]
15:06:00 DEBUG grep result      [matches=2]
15:06:01 INFO  edit file        [path="agent.go", old="foo()", new="bar()"]
15:06:01 DEBUG edit result      [ok=true]
15:06:02 INFO  bash command     [cmd="go build ./..."]
15:06:03 DEBUG bash result      [exit=0, out=""]
15:06:04 INFO  bash command     [cmd="go test ./internal/agent/..."]
15:06:08 DEBUG bash result      [exit=0, out="ok  \tphobos.org.uk/agency..."]
15:06:08 INFO  execution done   [duration_ms=12150, turns=5, cost_usd=0.0082]
15:06:08 INFO  task completed   [duration_seconds=12.15, input_tokens=8234, output_tokens=1523]
```

### Example: Web Search Task

A task involving web search would show:

```
15:10:00 INFO  task created     [model=sonnet, session_id=def456]
15:10:00 INFO  task started     [timeout_seconds=1800]
15:10:01 INFO  web search       [query="golang context timeout best practices 2025"]
15:10:02 DEBUG search result    [results=8]
15:10:03 INFO  web fetch        [url="https://go.dev/blog/context..."]
15:10:04 DEBUG fetch result     [bytes=15234]
15:10:05 INFO  read file        [path="internal/agent/agent.go"]
15:10:05 DEBUG read result      [bytes=24531]
15:10:07 INFO  edit file        [path="agent.go", old="ctx, cancel := cont...", new="ctx, cancel := cont..."]
15:10:07 DEBUG edit result      [ok=true]
15:10:08 INFO  execution done   [duration_ms=8200, turns=4, cost_usd=0.0065]
```

### Example: Subagent Task

A task that spawns subagents would show:

```
15:15:00 INFO  task created     [model=opus, session_id=ghi789]
15:15:00 INFO  task started     [timeout_seconds=3600]
15:15:01 INFO  spawn agent      [type="Explore", desc="find auth handlers"]
15:15:05 DEBUG agent result     [ok=true, chars=2341]
15:15:06 INFO  spawn agent      [type="Plan", desc="design auth refactor"]
15:15:12 DEBUG agent result     [ok=true, chars=4521]
15:15:13 INFO  read file        [path="internal/auth/handler.go"]
15:15:13 DEBUG read result      [bytes=8923]
15:15:15 INFO  edit file        [path="handler.go", old="func Login(w http...", new="func Login(ctx con..."]
15:15:15 DEBUG edit result      [ok=true]
15:15:16 INFO  execution done   [duration_ms=16100, turns=6, cost_usd=0.0234]
```

---

## Provider Abstraction Layer

To support future providers (OpenAI, Gemini, local models, etc.), the streaming log system uses a provider-agnostic event model.

### Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  Claude CLI     │     │  OpenAI API     │     │  Other Provider │
│  stream-json    │     │  (future)       │     │  (future)       │
└────────┬────────┘     └────────┬────────┘     └────────┬────────┘
         │                       │                       │
         ▼                       ▼                       ▼
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│ ClaudeParser    │     │ OpenAIParser    │     │ GenericParser   │
│ implements      │     │ implements      │     │ implements      │
│ StreamParser    │     │ StreamParser    │     │ StreamParser    │
└────────┬────────┘     └────────┬────────┘     └────────┬────────┘
         │                       │                       │
         └───────────────────────┴───────────────────────┘
                                 │
                                 ▼
                    ┌─────────────────────────┐
                    │   Normalized ToolEvent  │
                    │   (provider-agnostic)   │
                    └────────────┬────────────┘
                                 │
                                 ▼
                    ┌─────────────────────────┐
                    │   ToolEventLogger       │
                    │   (tool-specific logs)  │
                    └─────────────────────────┘
```

### Core Interfaces

```go
// ToolEvent represents a normalized tool interaction event
type ToolEvent struct {
    Type      ToolEventType          // ToolCall, ToolResult, SessionInit, Complete
    Timestamp time.Time

    // For ToolCall events
    ToolName  string                 // "Bash", "Read", etc.
    ToolID    string                 // Correlation ID
    Input     map[string]any         // Parsed tool input

    // For ToolResult events
    Output    string                 // Tool output (may be truncated for logging)
    IsError   bool                   // Whether tool failed

    // For Complete events
    Metrics   *CompletionMetrics     // Duration, turns, cost, tokens
}

type ToolEventType int
const (
    EventSessionInit ToolEventType = iota
    EventToolCall
    EventToolResult
    EventTextResponse
    EventComplete
)

type CompletionMetrics struct {
    DurationMS   int
    NumTurns     int
    TotalCostUSD float64
    InputTokens  int
    OutputTokens int
}

// StreamParser converts provider-specific stream format to ToolEvents
type StreamParser interface {
    // ParseLine parses a single line from the stream
    // Returns nil if line doesn't produce an event
    ParseLine(line []byte) (*ToolEvent, error)

    // Provider returns the provider name for logging
    Provider() string
}

// ToolEventLogger logs tool events in human-readable format
type ToolEventLogger interface {
    Log(event *ToolEvent)
}
```

### Provider Implementations

#### Claude Parser

```go
type ClaudeStreamParser struct {
    tracker *ToolCallTracker // correlates tool_use with tool_result
}

func (p *ClaudeStreamParser) Provider() string { return "claude" }

func (p *ClaudeStreamParser) ParseLine(line []byte) (*ToolEvent, error) {
    var raw ClaudeStreamEvent
    if err := json.Unmarshal(line, &raw); err != nil {
        return nil, err
    }

    switch raw.Type {
    case "system":
        if raw.Subtype == "init" {
            return &ToolEvent{Type: EventSessionInit, Timestamp: time.Now()}, nil
        }
    case "assistant":
        // Extract tool_use blocks, track them, emit ToolCall events
    case "user":
        // Match tool_result to tracked tool_use, emit ToolResult events
    case "result":
        return &ToolEvent{
            Type: EventComplete,
            Metrics: &CompletionMetrics{
                DurationMS:   raw.DurationMS,
                NumTurns:     raw.NumTurns,
                TotalCostUSD: raw.TotalCostUSD,
            },
        }, nil
    }
    return nil, nil
}
```

#### Future: OpenAI Parser (skeleton)

```go
type OpenAIStreamParser struct {
    tracker *ToolCallTracker
}

func (p *OpenAIStreamParser) Provider() string { return "openai" }

func (p *OpenAIStreamParser) ParseLine(line []byte) (*ToolEvent, error) {
    // OpenAI uses different event structure:
    // - "tool_calls" array in assistant message delta
    // - Tool results sent back as "tool" role messages
    // Normalize to same ToolEvent structure
    return nil, nil
}
```

### Tool-Specific Logging (Provider Agnostic)

The `ToolEventLogger` consumes normalized `ToolEvent`s and applies tool-specific formatting:

```go
type DefaultToolEventLogger struct {
    log *logging.TaskLogger
}

func (l *DefaultToolEventLogger) Log(event *ToolEvent) {
    switch event.Type {
    case EventToolCall:
        l.logToolCall(event)
    case EventToolResult:
        l.logToolResult(event)
    case EventComplete:
        l.logComplete(event)
    }
}

func (l *DefaultToolEventLogger) logToolCall(event *ToolEvent) {
    // Same tool-specific switch as before, but operates on
    // normalized event.ToolName and event.Input
    switch event.ToolName {
    case "Bash":
        l.log.Info("bash command", map[string]any{
            "cmd": truncate(event.Input["command"].(string), 64),
        })
    // ... other tools
    }
}
```

### Registration and Factory

```go
// ParserRegistry manages provider-specific parsers
var parserRegistry = map[string]func() StreamParser{
    "claude": func() StreamParser { return NewClaudeStreamParser() },
    // "openai": func() StreamParser { return NewOpenAIStreamParser() },
}

func NewStreamParser(provider string) (StreamParser, error) {
    factory, ok := parserRegistry[provider]
    if !ok {
        return nil, fmt.Errorf("unknown provider: %s", provider)
    }
    return factory(), nil
}
```

### Benefits of This Abstraction

1. **Single logging implementation** - Tool-specific formatting written once
2. **Easy provider addition** - Implement `StreamParser` interface only
3. **Testable** - Can inject mock parsers for unit tests
4. **Future-proof** - Supports providers with different streaming formats

---

### UI Considerations

The logs panel already supports displaying these entries. With more events:

1. **Pagination/Scrolling**: The 300px max-height with overflow-y scroll handles this
2. **Filtering by level**: Could add UI toggle for debug vs info only
3. **Real-time updates**: Polling already fetches latest logs every 2s during execution

### Migration

- Backwards compatible: Old agents without streaming still work
- New log events are additive
- No schema changes to logging package needed

### Future Enhancements

1. **Structured tool input logging**: Log sanitized tool inputs (redact secrets)
2. **Thinking/reasoning**: If Claude exposes thinking steps, log those
3. **Cost tracking per tool**: Aggregate cost by tool type
4. **Log export**: API to export logs for external analysis

## Files to Modify

1. `internal/agent/agent.go` - executeTask() streaming implementation
2. `internal/stream/event.go` - New: ToolEvent types and interfaces
3. `internal/stream/claude.go` - New: Claude stream parser implementation
4. `internal/stream/logger.go` - New: Tool-specific event logging
5. `internal/stream/stream_test.go` - New: Parser unit tests

## Testing

1. Unit tests for Claude stream parser with sample JSON lines
2. Unit tests for tool-specific log formatting
3. Integration test with mock Claude output (testdata/mock-claude-stream)
4. Smoke test verification that logs show tool calls

## Risks

1. **Performance**: Line-by-line parsing adds overhead (minimal for JSON lines)
2. **Log volume**: More events means more storage (ring buffer already limits)
3. **Claude CLI changes**: Stream format may change between versions
