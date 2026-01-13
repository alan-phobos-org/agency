#!/bin/bash
set -euo pipefail

VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS="-X main.version=$VERSION"
BINARIES=(ag-agent-claude ag-view-web ag-cli ag-scheduler)

# Helper functions
build_all() {
    echo "Building agency $VERSION..."
    for bin in "${BINARIES[@]}"; do
        go build -ldflags "$LDFLAGS" -o "bin/$bin" "./cmd/$bin"
    done
}

run_tests() {
    local tags="${1:-}"
    local flags="-race"
    [[ -n "$tags" ]] && flags+=" -tags=$tags"
    go test $flags ./...
}

check_service() {
    local url="$1" name="$2"
    if ! curl -sf -k "$url" > /dev/null; then
        echo "ERROR: $name not responding"
        ./deployment/stop-agency.sh
        exit 1
    fi
}

case "${1:-help}" in
    build)
        build_all
        ;;
    test)
        echo "Running unit tests..."
        go test -race -short ./...
        ;;
    test-all)
        echo "Running unit + integration tests..."
        run_tests
        run_tests integration
        ;;
    test-int)
        echo "Running integration tests..."
        run_tests integration
        ;;
    test-sys)
        echo "Running system tests..."
        build_all
        AGENCY_BIN_DIR="$(pwd)/bin" run_tests system
        ;;
    test-release)
        echo "Running full release test suite..."
        build_all
        run_tests
        run_tests integration
        AGENCY_BIN_DIR="$(pwd)/bin" run_tests system
        ;;
    dist)
        echo "Running full test suite before dist..."
        $0 lint
        $0 test-release
        echo "Building distribution package..."
        rm -rf dist/
        mkdir -p dist/bin dist/deployment dist/configs
        cp "${BINARIES[@]/#/bin/}" dist/bin/
        cp deployment/agency.sh deployment/stop-agency.sh deployment/deploy-agency.sh dist/deployment/
        cp configs/contexts.yaml configs/scheduler.yaml dist/configs/
        tar -czf "dist/agency-$VERSION.tar.gz" -C dist bin deployment configs
        echo "Created dist/agency-$VERSION.tar.gz"
        ;;
    lint)
        echo "Running linters..."
        gofmt -l -w .
        staticcheck ./... 2>/dev/null || echo "staticcheck not installed, skipping"
        ;;
    check)
        $0 lint && $0 test
        ;;
    clean)
        rm -rf bin/ dist/ coverage.out
        ;;
    deploy-local)
        $0 dist
        echo "Deploying..."
        exec ./dist/deployment/agency.sh
        ;;
    prepare-release)
        echo "=== Preparing release ==="
        echo ""

        echo "Step 1/5: Running build check..."
        $0 check
        echo "✓ Build check passed"
        echo ""

        echo "Step 2/5: Running full test suite..."
        $0 test-all
        echo "✓ Full test suite passed"
        echo ""

        echo "Step 3/5: Running system tests..."
        $0 test-sys
        echo "✓ System tests passed"
        echo ""

        echo "Step 4/5: Testing local deployment..."
        build_all
        ./deployment/agency.sh
        sleep 2

        AGENT_PORT="${AG_AGENT_PORT:-9000}"
        WEB_PORT="${AG_WEB_PORT:-8443}"
        check_service "http://localhost:$AGENT_PORT/status" "Agent"
        check_service "https://localhost:$WEB_PORT/status" "Web view"

        ./deployment/stop-agency.sh
        echo "✓ Local deploy test passed"
        echo ""

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
        RELEASE_VERSION="${2:-}"
        if [ -z "$RELEASE_VERSION" ]; then
            echo "Usage: $0 release <version>"
            echo "Example: $0 release 1.1.0"
            exit 1
        fi

        if ! echo "$RELEASE_VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
            echo "ERROR: Invalid version format. Use semantic versioning (e.g., 1.2.3)"
            exit 1
        fi

        TAG="v$RELEASE_VERSION"

        if git rev-parse "$TAG" >/dev/null 2>&1; then
            echo "ERROR: Tag $TAG already exists"
            exit 1
        fi

        if ! git diff --quiet HEAD -- . ':!CHANGELOG.md'; then
            echo "ERROR: Uncommitted changes exist (other than CHANGELOG.md)"
            echo "Please commit or stash changes before releasing"
            git status --short
            exit 1
        fi

        if ! grep -q "## \[$RELEASE_VERSION\]" CHANGELOG.md; then
            echo "ERROR: CHANGELOG.md does not contain entry for version $RELEASE_VERSION"
            echo "Please add a '## [$RELEASE_VERSION]' section to CHANGELOG.md"
            exit 1
        fi

        if git diff --quiet HEAD -- CHANGELOG.md; then
            echo "WARNING: CHANGELOG.md has no uncommitted changes"
            echo "Did you forget to update the changelog?"
            read -p "Continue anyway? [y/N] " -n 1 -r
            echo
            [[ ! $REPLY =~ ^[Yy]$ ]] && exit 1
        fi

        echo "Creating release $TAG..."

        if ! git diff --quiet HEAD -- CHANGELOG.md; then
            git add CHANGELOG.md
        fi

        if ! git diff --cached --quiet; then
            git commit -m "Release $TAG"
            echo "✓ Created release commit"
        else
            echo "No changes to commit"
        fi

        git tag -a "$TAG" -m "Release $TAG"
        echo "✓ Created tag $TAG"

        echo ""
        echo "=== Release $TAG created ==="
        echo ""
        echo "Next steps:"
        echo "  1. Review: git log -1 && git show $TAG"
        echo "  2. Push:   git push origin main $TAG"
        ;;
    status)
        echo "=== Project Status ==="
        echo ""

        # Version
        echo "Version: $VERSION"
        LAST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "none")
        echo "Latest release: $LAST_TAG"
        echo ""

        # Working copy
        echo "--- Working Copy ---"
        if git diff --quiet && git diff --cached --quiet; then
            UNTRACKED=$(git ls-files --others --exclude-standard | wc -l | tr -d ' ')
            if [ "$UNTRACKED" -eq 0 ]; then
                echo "Clean"
            else
                echo "Clean (${UNTRACKED} untracked files)"
            fi
        else
            echo "Dirty"
            git status --short | head -10
            TOTAL=$(git status --short | wc -l | tr -d ' ')
            [ "$TOTAL" -gt 10 ] && echo "... and $((TOTAL - 10)) more"
        fi
        echo ""

        # Remote sync
        echo "--- Remote Status ---"
        git fetch origin --quiet 2>/dev/null || echo "Warning: could not fetch origin"
        LOCAL=$(git rev-parse HEAD 2>/dev/null)
        REMOTE=$(git rev-parse origin/main 2>/dev/null || echo "unknown")
        if [ "$LOCAL" = "$REMOTE" ]; then
            echo "In sync with origin/main"
        else
            AHEAD=$(git rev-list --count origin/main..HEAD 2>/dev/null || echo "?")
            BEHIND=$(git rev-list --count HEAD..origin/main 2>/dev/null || echo "?")
            [ "$AHEAD" != "0" ] && echo "Ahead of origin/main by $AHEAD commits"
            [ "$BEHIND" != "0" ] && echo "Behind origin/main by $BEHIND commits"
        fi
        echo ""

        # CI status (requires gh CLI)
        echo "--- CI Status ---"
        if command -v gh &>/dev/null; then
            gh run list --limit 3 2>/dev/null || echo "Could not fetch CI status"
        else
            echo "gh CLI not installed - skipping CI status"
        fi
        echo ""

        # Recent releases
        echo "--- Recent Releases ---"
        if command -v gh &>/dev/null; then
            gh release list --limit 3 2>/dev/null || echo "No releases found"
        else
            git tag --sort=-creatordate | head -3 || echo "No tags found"
        fi
        echo ""

        # Changes since last release
        if [ "$LAST_TAG" != "none" ]; then
            echo "--- Changes Since $LAST_TAG ---"
            COMMIT_COUNT=$(git rev-list --count "$LAST_TAG"..HEAD 2>/dev/null || echo "0")
            echo "$COMMIT_COUNT commits"
            if [ "$COMMIT_COUNT" != "0" ] && [ "$COMMIT_COUNT" -lt 20 ]; then
                git log --oneline "$LAST_TAG"..HEAD
            elif [ "$COMMIT_COUNT" -ge 20 ]; then
                git log --oneline "$LAST_TAG"..HEAD | head -10
                echo "... and $((COMMIT_COUNT - 10)) more commits"
            fi
        fi
        ;;
    help|*)
        echo "Usage: $0 <target>"
        echo ""
        echo "Build targets:"
        echo "  build           Build all binaries (${BINARIES[*]}) to bin/"
        echo "  dist            Create distribution tarball with binaries, deployment scripts, and configs"
        echo "  clean           Remove bin/, dist/, and coverage.out"
        echo ""
        echo "Test targets:"
        echo "  test            Run unit tests only (fast, uses -short flag)"
        echo "  test-all        Run unit tests + integration tests"
        echo "  test-int        Run integration tests only"
        echo "  test-sys        Run system tests (builds first)"
        echo "  test-release    Run full test suite: unit + integration + system tests"
        echo ""
        echo "Code quality:"
        echo "  lint            Run gofmt and staticcheck"
        echo "  check           Run lint + unit tests (pre-commit check)"
        echo ""
        echo "Deployment:"
        echo "  deploy-local    Run full test suite, build dist, and deploy locally"
        echo ""
        echo "Release workflow:"
        echo "  prepare-release Run all checks, tests, and show changes since last release"
        echo "  release <ver>   Create release commit and tag (e.g., ./build.sh release 1.2.0)"
        echo ""
        echo "Status:"
        echo "  status          Show project status (working copy, remote, CI, releases)"
        echo ""
        echo "Current version: $VERSION"
        ;;
esac
