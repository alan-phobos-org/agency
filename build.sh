#!/bin/bash
set -euo pipefail

VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS="-X main.version=$VERSION"

case "${1:-help}" in
    build)
        echo "Building agency $VERSION..."
        go build -ldflags "$LDFLAGS" -o bin/ag-agent-claude ./cmd/ag-agent-claude
        go build -ldflags "$LDFLAGS" -o bin/ag-view-web ./cmd/ag-view-web
        go build -ldflags "$LDFLAGS" -o bin/ag-cli ./cmd/ag-cli
        ;;
    test)
        echo "Running unit tests..."
        go test -race -short ./...
        ;;
    test-all)
        echo "Running unit + integration tests..."
        go test -race ./...
        go test -race -tags=integration ./...
        ;;
    test-int)
        echo "Running integration tests..."
        go test -race -tags=integration ./...
        ;;
    test-sys)
        echo "Running system tests..."
        $0 build
        AGENCY_BIN_DIR="$(pwd)/bin" go test -race -tags=system ./...
        ;;
    test-release)
        echo "Running full release test suite..."
        $0 build
        go test -race ./...
        go test -race -tags=integration ./...
        AGENCY_BIN_DIR="$(pwd)/bin" go test -race -tags=system ./...
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
    deploy-local)
        $0 build
        exec ./deployment/agency.sh
        ;;
    prepare-release)
        # Run all checks and tests required before release
        echo "=== Preparing release ==="
        echo ""

        # Step 1: Build check (format, lint, unit tests)
        echo "Step 1/5: Running build check..."
        $0 check
        echo "✓ Build check passed"
        echo ""

        # Step 2: Full test suite
        echo "Step 2/5: Running full test suite..."
        $0 test-all
        echo "✓ Full test suite passed"
        echo ""

        # Step 3: System tests
        echo "Step 3/5: Running system tests..."
        $0 test-sys
        echo "✓ System tests passed"
        echo ""

        # Step 4: Local deploy test
        echo "Step 4/5: Testing local deployment..."
        $0 build

        # Start services
        ./deployment/agency.sh

        # Give extra time for full startup
        sleep 2

        # Verify both services respond
        AGENT_PORT="${AG_AGENT_PORT:-9000}"
        WEB_PORT="${AG_WEB_PORT:-8443}"

        if ! curl -sf "http://localhost:$AGENT_PORT/status" > /dev/null; then
            echo "ERROR: Agent not responding"
            ./deployment/stop-agency.sh
            exit 1
        fi

        if ! curl -sf -k "https://localhost:$WEB_PORT/status" > /dev/null; then
            echo "ERROR: Web view not responding"
            ./deployment/stop-agency.sh
            exit 1
        fi

        # Stop services
        ./deployment/stop-agency.sh
        echo "✓ Local deploy test passed"
        echo ""

        # Step 5: Show changes since last tag
        echo "Step 5/5: Changes since last release..."
        echo ""
        LAST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "")
        if [ -n "$LAST_TAG" ]; then
            echo "Last release: $LAST_TAG"
            echo ""
            echo "Commits since $LAST_TAG:"
            git log --oneline "$LAST_TAG"..HEAD
            echo ""
            echo "Files changed:"
            git diff --stat "$LAST_TAG"..HEAD | tail -1
        else
            echo "No previous release tag found"
            echo ""
            echo "All commits:"
            git log --oneline
        fi

        echo ""
        echo "=== Release preparation complete ==="
        echo ""
        echo "Next steps:"
        echo "  1. Review the changes above"
        echo "  2. Update CHANGELOG.md with release notes"
        echo "  3. Run: ./build.sh release <version>"
        echo ""
        echo "Example: ./build.sh release 1.1.0"
        ;;
    release)
        # Create a release commit and tag
        RELEASE_VERSION="${2:-}"

        if [ -z "$RELEASE_VERSION" ]; then
            echo "Usage: $0 release <version>"
            echo "Example: $0 release 1.1.0"
            exit 1
        fi

        # Validate version format (semver)
        if ! echo "$RELEASE_VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
            echo "ERROR: Invalid version format. Use semantic versioning (e.g., 1.2.3)"
            exit 1
        fi

        TAG="v$RELEASE_VERSION"

        # Check if tag already exists
        if git rev-parse "$TAG" >/dev/null 2>&1; then
            echo "ERROR: Tag $TAG already exists"
            exit 1
        fi

        # Check for uncommitted changes (excluding CHANGELOG.md which we expect to be modified)
        if ! git diff --quiet HEAD -- . ':!CHANGELOG.md'; then
            echo "ERROR: Uncommitted changes exist (other than CHANGELOG.md)"
            echo "Please commit or stash changes before releasing"
            git status --short
            exit 1
        fi

        # Check that CHANGELOG.md has an entry for this version
        if ! grep -q "## \[$RELEASE_VERSION\]" CHANGELOG.md; then
            echo "ERROR: CHANGELOG.md does not contain entry for version $RELEASE_VERSION"
            echo "Please add a '## [$RELEASE_VERSION]' section to CHANGELOG.md"
            exit 1
        fi

        # Check if CHANGELOG.md is modified (it should be, with the new version)
        if git diff --quiet HEAD -- CHANGELOG.md; then
            echo "WARNING: CHANGELOG.md has no uncommitted changes"
            echo "Did you forget to update the changelog?"
            read -p "Continue anyway? [y/N] " -n 1 -r
            echo
            if [[ ! $REPLY =~ ^[Yy]$ ]]; then
                exit 1
            fi
        fi

        echo "Creating release $TAG..."

        # Stage and commit CHANGELOG.md if modified
        if ! git diff --quiet HEAD -- CHANGELOG.md; then
            git add CHANGELOG.md
        fi

        # Create release commit (if there are staged changes)
        if ! git diff --cached --quiet; then
            git commit -m "Release $TAG"
            echo "✓ Created release commit"
        else
            echo "No changes to commit"
        fi

        # Create annotated tag
        git tag -a "$TAG" -m "Release $TAG"
        echo "✓ Created tag $TAG"

        echo ""
        echo "=== Release $TAG created ==="
        echo ""
        echo "Next steps:"
        echo "  1. Review: git log -1 && git show $TAG"
        echo "  2. Push:   git push origin main $TAG"
        ;;
    *)
        echo "Usage: $0 {build|test|test-all|test-int|test-sys|test-release|lint|check|clean|deploy-local|prepare-release|release}"
        ;;
esac
