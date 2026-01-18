# Streaming Task Logs Enhancement

## Overview

Currently, the agent logs only high-level task lifecycle events (created, started, completed). This document describes how to enhance observability by capturing and logging Claude CLI's streaming events in real-time.

## Current State

The agent captures these events:
- `task created` - When task is queued
- `task started` - When execution begins
- `task completed/failed` - When execution ends

This provides minimal visibility into what happens during task execution.

## Proposed Enhancement

### Claude CLI Stream JSON Format

Using `--output-format stream-json --verbose`, Claude CLI emits structured JSON events:

```json
// Session initialization
{"type":"system","subtype":"init","session_id":"...","tools":[...],"model":"..."}

// Assistant response (may include tool calls)
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{...}}]}}

// Tool execution result
{"type":"user","tool_use_result":"...","message":{"content":[{"type":"tool_result","content":"..."}]}}

// Final result
{"type":"result","subtype":"success","duration_ms":...,"num_turns":...,"total_cost_usd":...}
```

### Events to Log

| Event Type | Log Level | Fields |
|------------|-----------|--------|
| `system/init` | debug | model, tools count |
| `assistant` with `tool_use` | info | tool name, brief input summary |
| `user` with `tool_result` | debug | tool name, success/error, output length |
| `assistant` with `text` | debug | message length, truncated preview |
| `result` | info | duration, turns, cost |

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
        Content []struct {
            Type  string          `json:"type"`  // text, tool_use, tool_result
            Name  string          `json:"name"`  // tool name
            Text  string          `json:"text"`  // text content
            Input json.RawMessage `json:"input"` // tool input
        } `json:"content"`
    } `json:"message"`
    ToolUseResult string  `json:"tool_use_result"`
    DurationMS    int     `json:"duration_ms"`
    NumTurns      int     `json:"num_turns"`
    TotalCostUSD  float64 `json:"total_cost_usd"`
}

func (a *Agent) logStreamEvent(taskID string, event StreamEvent) {
    taskLog := a.log.WithTask(taskID)

    switch event.Type {
    case "system":
        if event.Subtype == "init" {
            taskLog.Debug("session initialized", nil)
        }

    case "assistant":
        for _, content := range event.Message.Content {
            switch content.Type {
            case "tool_use":
                taskLog.Info("tool call", map[string]any{
                    "tool": content.Name,
                })
            case "text":
                if len(content.Text) > 0 {
                    taskLog.Debug("assistant response", map[string]any{
                        "length": len(content.Text),
                    })
                }
            }
        }

    case "user":
        // Tool results - log at debug level
        if event.ToolUseResult != "" {
            taskLog.Debug("tool result", map[string]any{
                "length": len(event.ToolUseResult),
            })
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

### Example Log Output

With this enhancement, a task's logs would show:

```
15:05:56 INFO  task created     [model=haiku, session_id=abc123]
15:05:56 INFO  task started     [timeout_seconds=1800]
15:05:56 DEBUG session init     []
15:05:57 INFO  tool call        [tool=Bash]
15:05:57 DEBUG tool result      [length=1234]
15:05:58 INFO  tool call        [tool=Edit]
15:05:58 DEBUG tool result      [length=56]
15:05:59 INFO  tool call        [tool=Bash]
15:05:59 DEBUG tool result      [length=89]
15:06:01 INFO  execution done   [duration_ms=4815, turns=3, cost_usd=0.0074]
15:06:01 INFO  task completed   [duration_seconds=4.82, input_tokens=2, output_tokens=5]
```

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
2. `internal/agent/config.go` - Add StreamLogs config option
3. `internal/agent/stream.go` - New file for stream event parsing

## Testing

1. Unit tests for stream event parser
2. Integration test with mock Claude output
3. Smoke test verification that logs show tool calls

## Risks

1. **Performance**: Line-by-line parsing adds overhead (minimal for JSON lines)
2. **Log volume**: More events means more storage (ring buffer already limits)
3. **Claude CLI changes**: Stream format may change between versions
