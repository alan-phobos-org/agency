package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var version = "dev"

// createHTTPClient creates an HTTP client with TLS skip for localhost HTTPS
func createHTTPClient(timeout time.Duration, url string) *http.Client {
	client := &http.Client{Timeout: timeout}

	// Skip TLS verification for localhost HTTPS (self-signed certs)
	if strings.HasPrefix(url, "https://localhost") || strings.HasPrefix(url, "https://127.0.0.1") {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}

	return client
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "task":
		taskCmd(os.Args[2:])
	case "queue":
		queueCmd(os.Args[2:])
	case "queue-status":
		queueStatusCmd(os.Args[2:])
	case "queue-cancel":
		queueCancelCmd(os.Args[2:])
	case "status":
		statusCmd(os.Args[2:])
	case "discover":
		discoverCmd(os.Args[2:])
	case "version":
		fmt.Println(version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`ag-cli - Agency command-line interface

Usage:
  ag-cli <command> [flags]

Commands:
  task          Submit a task to an agent (direct)
  queue         Submit a task to the queue (via director)
  queue-status  Get queue status or specific queued task
  queue-cancel  Cancel a queued task
  status        Get status of an agent or component
  discover      Discover running components
  version       Show version
  help          Show this help

Run 'ag-cli <command> -h' for command-specific help.`)
}

// taskCmd handles the 'task' subcommand
func taskCmd(args []string) {
	fs := flag.NewFlagSet("task", flag.ExitOnError)
	agentURL := fs.String("agent", "http://localhost:9000", "Agent URL")
	tier := fs.String("tier", "standard", "Model tier (fast, standard, heavy)")
	agentKind := fs.String("agent-kind", "claude", "Agent kind (claude, codex)")
	timeout := fs.Duration("timeout", 30*time.Minute, "Task timeout")
	sessionID := fs.String("session", "", "Session ID to continue (optional)")
	fs.Parse(args)

	remaining := fs.Args()
	if len(remaining) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: ag-cli task [flags] <prompt>\n")
		fs.PrintDefaults()
		os.Exit(1)
	}
	prompt := remaining[0]

	client := createHTTPClient(5*time.Minute, *agentURL)

	// Submit task
	taskReq := map[string]interface{}{
		"prompt":          prompt,
		"timeout_seconds": int(timeout.Seconds()),
	}
	if *tier != "" {
		taskReq["tier"] = *tier
	}
	if *agentKind != "" {
		taskReq["agent_kind"] = *agentKind
	}
	if *sessionID != "" {
		taskReq["session_id"] = *sessionID
	}
	body, _ := json.Marshal(taskReq)

	resp, err := client.Post(*agentURL+"/task", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error submitting task: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Error: %s\n", respBody)
		os.Exit(1)
	}

	var taskResp struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&taskResp); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Task submitted: %s\n", taskResp.TaskID)

	// Poll for completion
	result := pollForCompletion(client, *agentURL, taskResp.TaskID, time.Hour)

	// Print result
	fmt.Printf("\n=== Task %s ===\n", result.TaskID)
	fmt.Printf("State: %s\n", result.State)
	fmt.Printf("Duration: %.2fs\n", result.DurationSeconds)

	if result.ExitCode != nil {
		fmt.Printf("Exit code: %d\n", *result.ExitCode)
	}

	if result.Error != nil {
		fmt.Printf("Error: [%s] %s\n", result.Error["type"], result.Error["message"])
	}

	if result.Output != "" {
		fmt.Printf("\n--- Output ---\n%s\n", result.Output)
	}

	if result.ExitCode != nil && *result.ExitCode != 0 {
		os.Exit(*result.ExitCode)
	}
}

type taskStatus struct {
	TaskID          string                 `json:"task_id"`
	State           string                 `json:"state"`
	ExitCode        *int                   `json:"exit_code"`
	Output          string                 `json:"output"`
	Error           map[string]interface{} `json:"error"`
	DurationSeconds float64                `json:"duration_seconds"`
}

func pollForCompletion(client *http.Client, agentURL, taskID string, timeout time.Duration) *taskStatus {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	deadline := time.After(timeout)

	for {
		select {
		case <-deadline:
			fmt.Fprintf(os.Stderr, "\nPolling timeout\n")
			os.Exit(1)
		case <-ticker.C:
			resp, err := client.Get(agentURL + "/task/" + taskID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\nError polling: %v\n", err)
				os.Exit(1)
			}

			var status taskStatus
			if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
				resp.Body.Close()
				fmt.Fprintf(os.Stderr, "\nError parsing status: %v\n", err)
				os.Exit(1)
			}
			resp.Body.Close()

			switch status.State {
			case "completed", "failed", "cancelled":
				fmt.Fprintf(os.Stderr, "\n")
				return &status
			case "working", "queued":
				fmt.Fprintf(os.Stderr, ".")
			default:
				fmt.Fprintf(os.Stderr, "\nUnknown state: %s\n", status.State)
				os.Exit(1)
			}
		}
	}
}

// statusCmd handles the 'status' subcommand
func statusCmd(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	url := fs.String("url", "http://localhost:9000", "Component URL")
	fs.Parse(args)

	// Allow URL as positional arg
	if remaining := fs.Args(); len(remaining) > 0 {
		*url = remaining[0]
	}

	client := createHTTPClient(5*time.Second, *url)
	resp, err := client.Get(*url + "/status")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var status map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing status: %v\n", err)
		os.Exit(1)
	}

	// Pretty print
	output, _ := json.MarshalIndent(status, "", "  ")
	fmt.Println(string(output))
}

// discoverCmd handles the 'discover' subcommand
func discoverCmd(args []string) {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	portStart := fs.Int("port-start", 9000, "Start of port range")
	portEnd := fs.Int("port-end", 9009, "End of port range")
	fs.Parse(args)

	fmt.Printf("Scanning ports %d-%d...\n\n", *portStart, *portEnd)

	found := 0
	for port := *portStart; port <= *portEnd; port++ {
		url := fmt.Sprintf("https://localhost:%d/status", port)
		client := createHTTPClient(500*time.Millisecond, url)
		resp, err := client.Get(url)
		if err != nil {
			continue
		}

		var status map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		found++
		compType := status["type"]
		if compType == nil {
			compType = "unknown"
		}
		agentKind := status["agent_kind"]
		state := status["state"]
		ver := status["version"]
		interfaces := status["interfaces"]

		fmt.Printf("  :%d  type=%-10v agent_kind=%-7v state=%-10v version=%-10v interfaces=%v\n",
			port, compType, agentKind, state, ver, interfaces)
	}

	if found == 0 {
		fmt.Println("No components found.")
	} else {
		fmt.Printf("\nFound %d component(s)\n", found)
	}
}

// queueCmd handles the 'queue' subcommand - submit task to queue
func queueCmd(args []string) {
	fs := flag.NewFlagSet("queue", flag.ExitOnError)
	directorURL := fs.String("director", "http://localhost:8080", "Director URL")
	model := fs.String("model", "", "Model override (provider-specific)")
	tier := fs.String("tier", "standard", "Model tier (fast, standard, heavy)")
	agentKind := fs.String("agent-kind", "claude", "Agent kind (claude, codex)")
	timeout := fs.Duration("timeout", 30*time.Minute, "Task timeout")
	source := fs.String("source", "cli", "Source identifier")
	fs.Parse(args)

	remaining := fs.Args()
	if len(remaining) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: ag-cli queue [flags] <prompt>\n")
		fs.PrintDefaults()
		os.Exit(1)
	}
	prompt := remaining[0]

	client := createHTTPClient(30*time.Second, *directorURL)

	// Submit to queue
	queueReq := map[string]interface{}{
		"prompt":          prompt,
		"timeout_seconds": int(timeout.Seconds()),
		"source":          *source,
	}
	if *model != "" {
		queueReq["model"] = *model
	}
	if *tier != "" {
		queueReq["tier"] = *tier
	}
	if *agentKind != "" {
		queueReq["agent_kind"] = *agentKind
	}
	body, _ := json.Marshal(queueReq)

	resp, err := client.Post(*directorURL+"/api/queue/task", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error submitting to queue: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusServiceUnavailable {
		fmt.Fprintf(os.Stderr, "Error: queue is at capacity\n")
		os.Exit(1)
	}

	if resp.StatusCode != http.StatusCreated {
		fmt.Fprintf(os.Stderr, "Error: %s\n", respBody)
		os.Exit(1)
	}

	var queueResp struct {
		QueueID  string `json:"queue_id"`
		Position int    `json:"position"`
		State    string `json:"state"`
	}
	if err := json.Unmarshal(respBody, &queueResp); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Queued: %s (position %d)\n", queueResp.QueueID, queueResp.Position)
}

// queueStatusCmd handles the 'queue-status' subcommand
func queueStatusCmd(args []string) {
	fs := flag.NewFlagSet("queue-status", flag.ExitOnError)
	directorURL := fs.String("director", "http://localhost:8080", "Director URL")
	fs.Parse(args)

	client := createHTTPClient(10*time.Second, *directorURL)

	// Check if specific queue ID provided
	remaining := fs.Args()
	if len(remaining) > 0 {
		// Get specific queued task
		queueID := remaining[0]
		resp, err := client.Get(*directorURL + "/api/queue/" + queueID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			fmt.Fprintf(os.Stderr, "Queued task not found: %s\n", queueID)
			os.Exit(1)
		}

		var task map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
			os.Exit(1)
		}

		output, _ := json.MarshalIndent(task, "", "  ")
		fmt.Println(string(output))
		return
	}

	// Get full queue status
	resp, err := client.Get(*directorURL + "/api/queue")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var queue struct {
		Depth            int     `json:"depth"`
		MaxSize          int     `json:"max_size"`
		OldestAgeSeconds float64 `json:"oldest_age_seconds"`
		DispatchedCount  int     `json:"dispatched_count"`
		Tasks            []struct {
			QueueID       string `json:"queue_id"`
			State         string `json:"state"`
			Position      int    `json:"position"`
			PromptPreview string `json:"prompt_preview"`
			Source        string `json:"source"`
		} `json:"tasks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&queue); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Queue: %d/%d pending, %d dispatched\n", queue.Depth, queue.MaxSize, queue.DispatchedCount)
	if queue.OldestAgeSeconds > 0 {
		fmt.Printf("Oldest task age: %.1fs\n", queue.OldestAgeSeconds)
	}
	fmt.Println()

	if len(queue.Tasks) == 0 {
		fmt.Println("No tasks in queue.")
		return
	}

	for _, task := range queue.Tasks {
		posStr := ""
		if task.Position > 0 {
			posStr = fmt.Sprintf("#%d ", task.Position)
		}
		fmt.Printf("  %s[%s] %s%s\n", task.QueueID, task.State, posStr, task.PromptPreview)
	}
}

// queueCancelCmd handles the 'queue-cancel' subcommand
func queueCancelCmd(args []string) {
	fs := flag.NewFlagSet("queue-cancel", flag.ExitOnError)
	directorURL := fs.String("director", "http://localhost:8080", "Director URL")
	fs.Parse(args)

	remaining := fs.Args()
	if len(remaining) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: ag-cli queue-cancel [flags] <queue_id>\n")
		fs.PrintDefaults()
		os.Exit(1)
	}
	queueID := remaining[0]

	client := createHTTPClient(10*time.Second, *directorURL)

	req, _ := http.NewRequest(http.MethodPost, *directorURL+"/api/queue/"+queueID+"/cancel", nil)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		fmt.Fprintf(os.Stderr, "Queued task not found: %s\n", queueID)
		os.Exit(1)
	}

	var result struct {
		QueueID       string `json:"queue_id"`
		State         string `json:"state"`
		WasDispatched bool   `json:"was_dispatched"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	if result.WasDispatched {
		fmt.Printf("Cancelled %s (was dispatched to agent)\n", result.QueueID)
	} else {
		fmt.Printf("Cancelled %s\n", result.QueueID)
	}
}
