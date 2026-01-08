package testutil

import (
	"fmt"
	"hash/fnv"
	"net/http"
	"testing"
	"time"
)

// AllocateTestPort returns a deterministic port based on test name
func AllocateTestPort(t *testing.T) int {
	t.Helper()
	return AllocateTestPortN(t, 0)
}

// AllocateTestPortN returns a deterministic port based on test name and index.
// Use different index values to get multiple unique ports within the same test.
func AllocateTestPortN(t *testing.T, n int) int {
	t.Helper()
	h := fnv.New32a()
	h.Write([]byte(t.Name()))
	h.Write([]byte{byte(n)})
	return 10000 + int(h.Sum32()%10000)
}

// WaitForHealthy waits for a URL to return 200 OK
func WaitForHealthy(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}

	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("Service at %s did not become healthy within %v", url, timeout)
}

// Eventually retries a condition until it returns true or timeout expires
func Eventually(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("Condition did not become true within timeout")
}

// MockClaudeScript returns a bash script that simulates Claude CLI
func MockClaudeScript(response string) string {
	return fmt.Sprintf(`#!/bin/bash
echo '%s'
`, response)
}

// SuccessResponse returns a mock Claude success JSON response
func SuccessResponse(result string) string {
	return fmt.Sprintf(`{"session_id":"test-session","result":%q,"exit_code":0,"usage":{"input_tokens":100,"output_tokens":50}}`, result)
}

// ErrorResponse returns a mock Claude error JSON response
func ErrorResponse(message string) string {
	return fmt.Sprintf(`{"session_id":"test-session","result":"","exit_code":1,"error":%q}`, message)
}
