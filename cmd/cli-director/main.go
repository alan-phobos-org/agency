package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/anthropics/agency/internal/director/cli"
)

var version = "dev"

func main() {
	agentURL := flag.String("agent", "http://localhost:9000", "Agent URL")
	workdir := flag.String("workdir", "", "Working directory for task")
	timeout := flag.Duration("timeout", 30*time.Minute, "Task timeout")
	showVersion := flag.Bool("version", false, "Show version")
	statusOnly := flag.Bool("status", false, "Show agent status and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	d := cli.New(*agentURL)

	if *statusOnly {
		status, err := d.Status()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting status: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Agent status: %v\n", status)
		os.Exit(0)
	}

	// Get prompt from args
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: director [flags] <prompt>\n")
		flag.PrintDefaults()
		os.Exit(1)
	}
	prompt := args[0]

	// Default workdir to current directory
	if *workdir == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
			os.Exit(1)
		}
		*workdir = wd
	}

	// Run the task
	result, err := d.Run(prompt, *workdir, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\n")

	// Print result
	fmt.Printf("\n=== Task %s ===\n", result.TaskID)
	fmt.Printf("State: %s\n", result.State)
	fmt.Printf("Duration: %.2fs\n", result.DurationSeconds)

	if result.ExitCode != nil {
		fmt.Printf("Exit code: %d\n", *result.ExitCode)
	}

	if result.Error != nil {
		fmt.Printf("Error: [%s] %s\n", result.Error.Type, result.Error.Message)
	}

	if result.Output != "" {
		fmt.Printf("\n--- Output ---\n%s\n", result.Output)
	}

	// Exit with task exit code
	if result.ExitCode != nil && *result.ExitCode != 0 {
		os.Exit(*result.ExitCode)
	}
}
