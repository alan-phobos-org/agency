package stream

import (
	"bytes"
	"testing"

	"phobos.org.uk/agency/internal/logging"
)

func TestClaudeStreamParser_SessionInit(t *testing.T) {
	t.Parallel()

	parser := NewClaudeStreamParser()

	line := []byte(`{"type":"system","subtype":"init","session_id":"test-123","model":"sonnet"}`)
	events, err := parser.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Type != EventSessionInit {
		t.Errorf("expected EventSessionInit, got %v", events[0].Type)
	}
}

func TestClaudeStreamParser_ToolUse(t *testing.T) {
	t.Parallel()

	parser := NewClaudeStreamParser()

	line := []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01X","name":"Bash","input":{"command":"git status"}}]}}`)
	events, err := parser.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	event := events[0]
	if event.Type != EventToolCall {
		t.Errorf("expected EventToolCall, got %v", event.Type)
	}
	if event.ToolName != "Bash" {
		t.Errorf("expected Bash, got %s", event.ToolName)
	}
	if event.ToolID != "toolu_01X" {
		t.Errorf("expected toolu_01X, got %s", event.ToolID)
	}
	if cmd, ok := event.Input["command"].(string); !ok || cmd != "git status" {
		t.Errorf("expected command 'git status', got %v", event.Input["command"])
	}
}

func TestClaudeStreamParser_ToolResult(t *testing.T) {
	t.Parallel()

	parser := NewClaudeStreamParser()

	// First, send the tool_use to track it
	toolUseLine := []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01X","name":"Read","input":{"file_path":"/test.go"}}]}}`)
	_, err := parser.ParseLine(toolUseLine)
	if err != nil {
		t.Fatalf("unexpected error on tool_use: %v", err)
	}

	// Now send the tool_result
	resultLine := []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01X","content":"file contents here"}]}}`)
	events, err := parser.ParseLine(resultLine)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	event := events[0]
	if event.Type != EventToolResult {
		t.Errorf("expected EventToolResult, got %v", event.Type)
	}
	if event.ToolName != "Read" {
		t.Errorf("expected Read, got %s", event.ToolName)
	}
	if event.Output != "file contents here" {
		t.Errorf("expected 'file contents here', got %s", event.Output)
	}
	if event.IsError {
		t.Error("expected IsError=false")
	}
}

func TestClaudeStreamParser_ToolResultError(t *testing.T) {
	t.Parallel()

	parser := NewClaudeStreamParser()

	// Track the tool_use
	toolUseLine := []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_err","name":"Bash","input":{"command":"false"}}]}}`)
	_, _ = parser.ParseLine(toolUseLine)

	// Send error result
	resultLine := []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_err","content":"command failed","is_error":true}]}}`)
	events, err := parser.ParseLine(resultLine)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	event := events[0]
	if !event.IsError {
		t.Error("expected IsError=true")
	}
}

func TestClaudeStreamParser_TextResponse(t *testing.T) {
	t.Parallel()

	parser := NewClaudeStreamParser()

	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Here is my response to your question."}]}}`)
	events, err := parser.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	event := events[0]
	if event.Type != EventTextResponse {
		t.Errorf("expected EventTextResponse, got %v", event.Type)
	}
	if event.TextLength != 37 {
		t.Errorf("expected TextLength=37, got %d", event.TextLength)
	}
}

func TestClaudeStreamParser_Result(t *testing.T) {
	t.Parallel()

	parser := NewClaudeStreamParser()

	line := []byte(`{"type":"result","subtype":"success","duration_ms":5000,"num_turns":3,"total_cost_usd":0.005}`)
	events, err := parser.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	event := events[0]
	if event.Type != EventComplete {
		t.Errorf("expected EventComplete, got %v", event.Type)
	}
	if event.Metrics == nil {
		t.Fatal("expected Metrics to be set")
	}
	if event.Metrics.DurationMS != 5000 {
		t.Errorf("expected DurationMS=5000, got %d", event.Metrics.DurationMS)
	}
	if event.Metrics.NumTurns != 3 {
		t.Errorf("expected NumTurns=3, got %d", event.Metrics.NumTurns)
	}
	if event.Metrics.TotalCostUSD != 0.005 {
		t.Errorf("expected TotalCostUSD=0.005, got %f", event.Metrics.TotalCostUSD)
	}
}

func TestClaudeStreamParser_ParallelToolCalls(t *testing.T) {
	t.Parallel()

	parser := NewClaudeStreamParser()

	// Multiple tool calls in one message
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01","name":"Read","input":{"file_path":"/a.go"}},{"type":"tool_use","id":"toolu_02","name":"Grep","input":{"pattern":"func"}}]}}`)
	events, err := parser.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].ToolName != "Read" {
		t.Errorf("expected first tool Read, got %s", events[0].ToolName)
	}
	if events[1].ToolName != "Grep" {
		t.Errorf("expected second tool Grep, got %s", events[1].ToolName)
	}
}

func TestClaudeStreamParser_ContentArray(t *testing.T) {
	t.Parallel()

	parser := NewClaudeStreamParser()

	// Track the tool_use
	toolUseLine := []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_arr","name":"Bash","input":{"command":"ls"}}]}}`)
	_, _ = parser.ParseLine(toolUseLine)

	// Send result with array content
	resultLine := []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_arr","content":[{"type":"text","text":"file1.go\n"},{"type":"text","text":"file2.go\n"}]}]}}`)
	events, err := parser.ParseLine(resultLine)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	expected := "file1.go\nfile2.go\n"
	if events[0].Output != expected {
		t.Errorf("expected %q, got %q", expected, events[0].Output)
	}
}

func TestClaudeStreamParser_EmptyLine(t *testing.T) {
	t.Parallel()

	parser := NewClaudeStreamParser()

	events, err := parser.ParseLine([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if events != nil {
		t.Errorf("expected nil events for empty line, got %v", events)
	}
}

func TestToolEventLogger_Bash(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := logging.New(logging.Config{
		Output:     &buf,
		Level:      logging.LevelDebug,
		Component:  "test",
		MaxEntries: 100,
	})

	taskLog := log.WithTask("test-task")
	logger := NewToolEventLogger(taskLog)

	// Log a bash command
	logger.Log(&ToolEvent{
		Type:     EventToolCall,
		ToolName: "Bash",
		Input:    map[string]any{"command": "git status --short"},
	})

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("bash command")) {
		t.Errorf("expected 'bash command' in output, got %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("git status --short")) {
		t.Errorf("expected command in output, got %s", output)
	}
}

func TestToolEventLogger_Read(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := logging.New(logging.Config{
		Output:     &buf,
		Level:      logging.LevelDebug,
		Component:  "test",
		MaxEntries: 100,
	})

	taskLog := log.WithTask("test-task")
	logger := NewToolEventLogger(taskLog)

	// Log a read call with offset/limit
	logger.Log(&ToolEvent{
		Type:     EventToolCall,
		ToolName: "Read",
		Input: map[string]any{
			"file_path": "/tmp/test.go",
			"offset":    float64(10),
			"limit":     float64(50),
		},
	})

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("read file")) {
		t.Errorf("expected 'read file' in output, got %s", output)
	}
}

func TestToolEventLogger_Edit(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := logging.New(logging.Config{
		Output:     &buf,
		Level:      logging.LevelDebug,
		Component:  "test",
		MaxEntries: 100,
	})

	taskLog := log.WithTask("test-task")
	logger := NewToolEventLogger(taskLog)

	logger.Log(&ToolEvent{
		Type:     EventToolCall,
		ToolName: "Edit",
		Input: map[string]any{
			"file_path":  "/path/to/file.go",
			"old_string": "func oldName()",
			"new_string": "func newName()",
		},
	})

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("edit file")) {
		t.Errorf("expected 'edit file' in output, got %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("file.go")) {
		t.Errorf("expected basename in output, got %s", output)
	}
}

func TestToolEventLogger_Task(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := logging.New(logging.Config{
		Output:     &buf,
		Level:      logging.LevelDebug,
		Component:  "test",
		MaxEntries: 100,
	})

	taskLog := log.WithTask("test-task")
	logger := NewToolEventLogger(taskLog)

	logger.Log(&ToolEvent{
		Type:     EventToolCall,
		ToolName: "Task",
		Input: map[string]any{
			"subagent_type": "Explore",
			"description":   "find authentication handlers in the codebase",
		},
	})

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("spawn agent")) {
		t.Errorf("expected 'spawn agent' in output, got %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("Explore")) {
		t.Errorf("expected 'Explore' in output, got %s", output)
	}
}

func TestToolEventLogger_Complete(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := logging.New(logging.Config{
		Output:     &buf,
		Level:      logging.LevelDebug,
		Component:  "test",
		MaxEntries: 100,
	})

	taskLog := log.WithTask("test-task")
	logger := NewToolEventLogger(taskLog)

	logger.Log(&ToolEvent{
		Type: EventComplete,
		Metrics: &CompletionMetrics{
			DurationMS:   12500,
			NumTurns:     5,
			TotalCostUSD: 0.0123,
		},
	})

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("execution complete")) {
		t.Errorf("expected 'execution complete' in output, got %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("12500")) {
		t.Errorf("expected duration in output, got %s", output)
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		max      int
		expected string
	}{
		{"short", 10, "short"},
		{"exactly ten", 11, "exactly ten"},
		{"this is a long string", 10, "this is a ..."},
		{"", 5, ""},
	}

	for _, tc := range tests {
		result := truncate(tc.input, tc.max)
		if result != tc.expected {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.input, tc.max, result, tc.expected)
		}
	}
}

func TestCountLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"single line", 0},
		{"line1\nline2", 1},
		{"line1\nline2\nline3\n", 3},
	}

	for _, tc := range tests {
		result := countLines(tc.input)
		if result != tc.expected {
			t.Errorf("countLines(%q) = %d, want %d", tc.input, result, tc.expected)
		}
	}
}
