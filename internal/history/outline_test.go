package history

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractSteps_SimpleText(t *testing.T) {
	t.Parallel()

	output := []byte("This is a simple text response")
	steps := ExtractSteps(output)

	require.Len(t, steps, 1)
	require.Equal(t, "text", steps[0].Type)
	require.Equal(t, "This is a simple text response", steps[0].OutputPreview)
}

func TestExtractSteps_ToolCall(t *testing.T) {
	t.Parallel()

	output := []byte(`[{
		"role": "assistant",
		"content": [
			{
				"type": "text",
				"text": "Let me read the file"
			},
			{
				"type": "tool_use",
				"id": "tool_1",
				"name": "Read",
				"input": {"file_path": "/src/main.go"}
			}
		]
	}, {
		"role": "user",
		"content": [
			{
				"type": "tool_result",
				"tool_use_id": "tool_1",
				"content": "package main\n\nfunc main() {\n\tfmt.Println(\"Hello\")\n}"
			}
		]
	}]`)

	steps := ExtractSteps(output)

	require.Len(t, steps, 2)

	require.Equal(t, "text", steps[0].Type)
	require.Equal(t, "Let me read the file", steps[0].OutputPreview)

	require.Equal(t, "tool_call", steps[1].Type)
	require.Equal(t, "Read", steps[1].Tool)
	require.Contains(t, steps[1].InputPreview, "file_path")
	require.Contains(t, steps[1].OutputPreview, "package main")
}

func TestExtractSteps_MultipleTools(t *testing.T) {
	t.Parallel()

	output := []byte(`[{
		"role": "assistant",
		"content": [
			{
				"type": "tool_use",
				"id": "tool_1",
				"name": "Read",
				"input": {"file_path": "/src/a.go"}
			},
			{
				"type": "tool_use",
				"id": "tool_2",
				"name": "Edit",
				"input": {"file_path": "/src/a.go", "old_string": "foo", "new_string": "bar"}
			}
		]
	}, {
		"role": "user",
		"content": [
			{
				"type": "tool_result",
				"tool_use_id": "tool_1",
				"content": "file contents"
			},
			{
				"type": "tool_result",
				"tool_use_id": "tool_2",
				"content": "Successfully edited"
			}
		]
	}]`)

	steps := ExtractSteps(output)

	require.Len(t, steps, 2)
	require.Equal(t, "Read", steps[0].Tool)
	require.Equal(t, "Edit", steps[1].Tool)
}

func TestExtractSteps_Truncation(t *testing.T) {
	t.Parallel()

	// Create text longer than PreviewLength
	longText := make([]byte, 300)
	for i := range longText {
		longText[i] = 'x'
	}

	steps := ExtractSteps(longText)

	require.Len(t, steps, 1)
	require.True(t, steps[0].Truncated)
	require.Len(t, steps[0].OutputPreview, PreviewLength+3) // +3 for "..."
}

func TestExtractSteps_InvalidJSON(t *testing.T) {
	t.Parallel()

	output := []byte("not valid json at all")
	steps := ExtractSteps(output)

	require.Len(t, steps, 1)
	require.Equal(t, "text", steps[0].Type)
	require.Equal(t, "not valid json at all", steps[0].OutputPreview)
}

func TestExtractSteps_EmptyOutput(t *testing.T) {
	t.Parallel()

	steps := ExtractSteps([]byte{})

	require.Len(t, steps, 1)
	require.Equal(t, "text", steps[0].Type)
	require.Equal(t, "", steps[0].OutputPreview)
}
