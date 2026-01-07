package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "task":
		taskCmd(os.Args[2:])
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
  task       Submit a task to an agent
  status     Get status of an agent or component
  discover   Discover running components
  version    Show version
  help       Show this help

Run 'ag-cli <command> -h' for command-specific help.`)
}

// taskCmd handles the 'task' subcommand
func taskCmd(args []string) {
	fs := flag.NewFlagSet("task", flag.ExitOnError)
	agentURL := fs.String("agent", "http://localhost:9000", "Agent URL")
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

	client := &http.Client{Timeout: 5 * time.Minute}

	// Submit task
	taskReq := map[string]interface{}{
		"prompt":          prompt,
		"timeout_seconds": int(timeout.Seconds()),
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
	json.NewDecoder(resp.Body).Decode(&taskResp)
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
			json.NewDecoder(resp.Body).Decode(&status)
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

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(*url + "/status")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var status map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&status)

	// Pretty print
	output, _ := json.MarshalIndent(status, "", "  ")
	fmt.Println(string(output))
}

// discoverCmd handles the 'discover' subcommand
func discoverCmd(args []string) {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	portStart := fs.Int("port-start", 9000, "Start of port range")
	portEnd := fs.Int("port-end", 9199, "End of port range")
	fs.Parse(args)

	client := &http.Client{Timeout: 500 * time.Millisecond}

	fmt.Printf("Scanning ports %d-%d...\n\n", *portStart, *portEnd)

	found := 0
	for port := *portStart; port <= *portEnd; port++ {
		url := fmt.Sprintf("http://localhost:%d/status", port)
		resp, err := client.Get(url)
		if err != nil {
			continue
		}

		var status map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()

		found++
		compType := status["type"]
		if compType == nil {
			compType = "unknown"
		}
		state := status["state"]
		ver := status["version"]
		interfaces := status["interfaces"]

		fmt.Printf("  :%d  type=%-10v state=%-10v version=%-10v interfaces=%v\n",
			port, compType, state, ver, interfaces)
	}

	if found == 0 {
		fmt.Println("No components found.")
	} else {
		fmt.Printf("\nFound %d component(s)\n", found)
	}
}
