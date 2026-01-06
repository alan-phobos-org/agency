#!/bin/bash
set -euo pipefail

VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS="-X main.version=$VERSION"

case "${1:-help}" in
    build)
        echo "Building agency $VERSION..."
        go build -ldflags "$LDFLAGS" -o bin/agency ./cmd/agency
        go build -ldflags "$LDFLAGS" -o bin/claude-agent ./cmd/claude-agent
        go build -ldflags "$LDFLAGS" -o bin/cli-director ./cmd/cli-director
        ;;
    test)
        echo "Running unit tests..."
        go test -race -short ./...
        ;;
    test-all)
        echo "Running all tests..."
        go test -race ./...
        ;;
    test-int)
        echo "Running integration tests..."
        go test -race -tags=integration ./...
        ;;
    test-sys)
        echo "Running system tests..."
        $0 build
        go test -race -tags=system ./...
        ;;
    lint)
        echo "Running linters..."
        gofmt -l -w .
        staticcheck ./... 2>/dev/null || echo "staticcheck not installed, skipping"
        ;;
    check)
        # Full pre-commit check
        $0 lint && $0 test
        ;;
    clean)
        rm -rf bin/ coverage.out
        ;;
    *)
        echo "Usage: $0 {build|test|test-all|test-int|test-sys|lint|check|clean}"
        ;;
esac
