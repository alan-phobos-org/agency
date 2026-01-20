#!/bin/bash
set -euo pipefail

VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS="-X main.version=$VERSION"
BINARIES=(ag-agent-claude ag-agent-codex ag-view-web ag-cli ag-scheduler)

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

detect_running_agency() {
    # Check common port sets to detect which mode is running
    if curl -sf "http://localhost:8080/api/status" >/dev/null 2>&1; then
        echo "dev"
        return 0
    fi
    if curl -sf "http://localhost:9080/api/status" >/dev/null 2>&1; then
        echo "prod"
        return 0
    fi
    if curl -sf "http://localhost:18080/api/status" >/dev/null 2>&1; then
        echo "smoke"
        return 0
    fi
    return 1
}

check_smoke_ports() {
    local ports=(18080 18443 19000 19001 19010)
    local conflicts=()

    for port in "${ports[@]}"; do
        if lsof -ti :$port >/dev/null 2>&1; then
            conflicts+=($port)
        fi
    done

    if [ ${#conflicts[@]} -gt 0 ]; then
        echo "ERROR: Smoke test ports in use: ${conflicts[*]}"
        echo ""
        echo "Options:"
        echo "  1. Clean up smoke test ports: for port in ${conflicts[*]}; do lsof -ti :\$port | xargs kill -9; done"
        echo "  2. Stop agency deployment: ./deployment/stop-agency.sh [dev|prod|smoke]"
        echo ""
        read -p "Auto-cleanup? [y/N] " -r response
        if [[ "$response" =~ ^[Yy]$ ]]; then
            for port in "${conflicts[@]}"; do
                lsof -ti :$port | xargs kill -9 2>/dev/null || true
            done
            sleep 2
            echo "Ports cleaned, retrying..."
        else
            return 1
        fi
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
        cp deployment/agency.sh deployment/stop-agency.sh deployment/deploy-agency.sh deployment/ports.conf dist/deployment/
        cp configs/scheduler.yaml dist/configs/
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
        # Stop any existing instance first
        ./deployment/stop-agency.sh dev 2>/dev/null || true

        DEPLOY_STEP=""
        deploy_fail() {
            echo ""
            echo "=== DEPLOY-LOCAL FAILED ==="
            echo "Step: $DEPLOY_STEP"
            echo ""
            echo "Troubleshooting:"
            case "$DEPLOY_STEP" in
                "lint")
                    echo "  - Run './build.sh lint' to see formatting issues"
                    echo "  - Run 'gofmt -d .' to see specific diffs"
                    ;;
                "build")
                    echo "  - Check for Go compilation errors above"
                    echo "  - Run 'go build ./...' for more details"
                    ;;
                "unit tests")
                    echo "  - Run './build.sh test' to re-run unit tests"
                    echo "  - Run 'go test -v ./...' for verbose output"
                    ;;
                "integration tests")
                    echo "  - Run './build.sh test-int' to re-run integration tests"
                    ;;
                "system tests")
                    echo "  - Run './build.sh test-sys' to re-run system tests"
                    ;;
                "dist packaging")
                    echo "  - Check that all required files exist"
                    ;;
                *)
                    echo "  - Review the error message above"
                    ;;
            esac
            exit 1
        }
        trap deploy_fail ERR

        echo "=== Deploy Local (dev mode) ==="
        echo ""

        DEPLOY_STEP="lint"
        echo "Step 1/6: Linting..."
        $0 lint

        DEPLOY_STEP="build"
        echo "Step 2/6: Building binaries..."
        build_all

        DEPLOY_STEP="unit tests"
        echo "Step 3/6: Running unit tests..."
        go test -race -short ./...

        DEPLOY_STEP="integration tests"
        echo "Step 4/6: Running integration tests..."
        run_tests integration

        DEPLOY_STEP="system tests"
        echo "Step 5/6: Running system tests..."
        AGENCY_BIN_DIR="$(pwd)/bin" run_tests system

        DEPLOY_STEP="dist packaging"
        echo "Step 6/6: Creating distribution..."
        rm -rf dist/
        mkdir -p dist/bin dist/deployment dist/configs
        cp "${BINARIES[@]/#/bin/}" dist/bin/
        cp deployment/agency.sh deployment/stop-agency.sh deployment/deploy-agency.sh deployment/ports.conf dist/deployment/
        cp configs/scheduler.yaml dist/configs/
        [ -f .env ] && cp .env dist/

        trap - ERR
        echo ""
        echo "Starting services..."
        if ! ./dist/deployment/agency.sh dev; then
            echo ""
            echo "=== DEPLOY-LOCAL FAILED: start services ==="
            echo ""
            # Check logs for port conflicts
            for log in dist/deployment/{scheduler,agent,view}-dev.log; do
                if [ -f "$log" ] && grep -q "address already in use" "$log"; then
                    PORT=$(grep "address already in use" "$log" | grep -oE ':[0-9]+' | tr -d ':' | head -1)
                    echo "Port $PORT is already in use."
                    echo "  lsof -i :$PORT        # find what's using it"
                    echo "  kill \$(lsof -ti :$PORT)  # kill the process"
                    exit 1
                fi
            done
            echo "Check logs: dist/deployment/{view,agent,scheduler}-dev.log"
            exit 1
        fi
        ;;
    deploy-local-quick)
        # Fast deployment - skips integration and system tests
        # Useful for rapid iteration during development
        ./deployment/stop-agency.sh dev 2>/dev/null || true

        DEPLOY_STEP=""
        deploy_fail() {
            echo ""
            echo "=== DEPLOY-LOCAL-QUICK FAILED ==="
            echo "Step: $DEPLOY_STEP"
            echo ""
            echo "Troubleshooting:"
            case "$DEPLOY_STEP" in
                "lint")
                    echo "  - Run './build.sh lint' to see formatting issues"
                    ;;
                "build")
                    echo "  - Check for Go compilation errors above"
                    ;;
                "unit tests")
                    echo "  - Run './build.sh test' to re-run unit tests"
                    echo "  - Run 'go test -v ./...' for verbose output"
                    ;;
                "dist packaging")
                    echo "  - Check that all required files exist"
                    ;;
                *)
                    echo "  - Review the error message above"
                    ;;
            esac
            exit 1
        }
        trap deploy_fail ERR

        echo "=== Deploy Local Quick (dev mode - skipping integration/system tests) ==="
        echo ""

        DEPLOY_STEP="lint"
        echo "Step 1/4: Linting..."
        $0 lint

        DEPLOY_STEP="build"
        echo "Step 2/4: Building binaries..."
        build_all

        DEPLOY_STEP="unit tests"
        echo "Step 3/4: Running unit tests..."
        go test -race -short ./...

        DEPLOY_STEP="dist packaging"
        echo "Step 4/4: Creating distribution..."
        rm -rf dist/
        mkdir -p dist/bin dist/deployment dist/configs
        cp "${BINARIES[@]/#/bin/}" dist/bin/
        cp deployment/agency.sh deployment/stop-agency.sh deployment/deploy-agency.sh deployment/ports.conf dist/deployment/
        cp configs/scheduler.yaml dist/configs/
        [ -f .env ] && cp .env dist/

        trap - ERR
        echo ""
        echo "Starting services..."
        if ! ./dist/deployment/agency.sh dev; then
            echo ""
            echo "=== DEPLOY-LOCAL-QUICK FAILED: start services ==="
            echo ""
            for log in dist/deployment/{scheduler,agent,view}-dev.log; do
                if [ -f "$log" ] && grep -q "address already in use" "$log"; then
                    PORT=$(grep "address already in use" "$log" | grep -oE ':[0-9]+' | tr -d ':' | head -1)
                    echo "Port $PORT is already in use."
                    echo "  lsof -i :$PORT        # find what's using it"
                    echo "  kill \$(lsof -ti :$PORT)  # kill the process"
                    exit 1
                fi
            done
            echo "Check logs: dist/deployment/{view,agent,scheduler}-dev.log"
            exit 1
        fi
        ;;
    stop-local)
        echo "Stopping local dev instance..."
        ./deployment/stop-agency.sh dev
        ;;
    quick-test)
        # Fast iteration cycle: build + deploy + smoke test
        echo "=== Quick Test Cycle (build + deploy-quick + smoke) ==="
        $0 stop-local
        $0 deploy-local-quick
        echo ""
        echo "Deployment complete. Running smoke tests..."
        $0 test-smoke
        ;;
    deploy-prod)
        # Usage: ./build.sh deploy-prod [host] [ssh-port] [ssh-key]
        # Reads DEPLOY_HOST, DEPLOY_PORT, DEPLOY_KEY from .env as defaults
        shift
        [ -f .env ] && source .env
        HOST="${1:-$DEPLOY_HOST}"
        PORT="${2:-${DEPLOY_PORT:-22}}"
        KEY="${3:-$DEPLOY_KEY}"
        if [ -z "$HOST" ]; then
            echo "Usage: $0 deploy-prod [host] [ssh-port] [ssh-key]"
            echo "  Or set DEPLOY_HOST, DEPLOY_PORT, DEPLOY_KEY in .env"
            exit 1
        fi
        exec ./deployment/deploy-agency.sh "$HOST" prod "$PORT" "$KEY"
        ;;
    stop-prod)
        # Usage: ./build.sh stop-prod [host] [ssh-port] [ssh-key]
        # Reads DEPLOY_HOST, DEPLOY_PORT, DEPLOY_KEY from .env as defaults
        shift
        [ -f .env ] && source .env
        HOST="${1:-$DEPLOY_HOST}"
        SSH_PORT="${2:-${DEPLOY_PORT:-22}}"
        SSH_KEY="${3:-$DEPLOY_KEY}"
        if [ -z "$HOST" ]; then
            echo "Usage: $0 stop-prod [host] [ssh-port] [ssh-key]"
            echo "  Or set DEPLOY_HOST, DEPLOY_PORT, DEPLOY_KEY in .env"
            exit 1
        fi
        SSH_OPTS="-C -p $SSH_PORT"
        [ -n "$SSH_KEY" ] && SSH_OPTS="$SSH_OPTS -i $SSH_KEY"
        # Load ports.conf to get REMOTE_DIR
        source ./deployment/ports.conf
        set_agency_env prod
        echo "Stopping agency on $HOST..."
        ssh $SSH_OPTS "$HOST" "$REMOTE_DIR/stop.sh"
        ;;
    test-smoke)
        echo "=== Smoke Test ==="
        START_TIME=$(date +%s)

        # Isolated ports to avoid conflicts
        export AG_WEB_PORT=18443
        export AG_WEB_INTERNAL_PORT=18080
        export AG_AGENT_PORT=19000
        export AG_AGENT_CODEX_PORT=19001
        export AG_SCHEDULER_PORT=19010
        export AG_DISCOVERY_START=19000
        export AG_DISCOVERY_END=19010
        export AG_SCHEDULER_CONFIG="$PWD/tests/smoke/fixtures/scheduler-smoke.yaml"
        export AG_WEB_PASSWORD="${AG_WEB_PASSWORD:-smoketest}"
        export AGENCY_PROMPTS_DIR="$PWD/tests/smoke/fixtures/prompts"
        export AGENCY_MODE="dev"

        # Check if agency is already running
        if mode=$(detect_running_agency 2>/dev/null); then
            echo "WARNING: Agency is running in $mode mode"
            echo "Smoke tests need ports 18080, 18443, 19000, 19001, 19010"
            echo ""
            if [ "$mode" = "smoke" ]; then
                echo "Shutting down existing smoke instance..."
                ./deployment/stop-agency.sh smoke
                sleep 2
            else
                echo "Please stop the $mode deployment first: ./deployment/stop-agency.sh $mode"
                exit 1
            fi
        fi

        # Check for port conflicts before starting
        check_smoke_ports

        # Cleanup on any exit
        cleanup_smoke_test() {
            echo "Cleaning up smoke test..."

            # Try graceful shutdown first
            if curl -sf -X POST "http://localhost:18080/shutdown" >/dev/null 2>&1; then
                echo "  Graceful shutdown initiated"
                sleep 2
            fi

            # Kill by PID file
            local pid_file="deployment/agency-smoke.pids"
            if [ -f "$pid_file" ]; then
                while read -r pid; do
                    kill "$pid" 2>/dev/null || true
                done < "$pid_file"
                rm -f "$pid_file"
            fi

            # Force kill any remaining processes on smoke ports
            for port in 18080 18443 19000 19001 19010; do
                lsof -ti :$port 2>/dev/null | xargs kill -9 2>/dev/null || true
            done

            echo "Cleanup complete"
        }
        trap cleanup_smoke_test EXIT ERR INT TERM

        # Build and start
        build_all
        ./deployment/agency.sh smoke

        # Run Playwright tests
        cd tests/smoke
        npm ci --silent
        npx playwright test --reporter=list
        TEST_EXIT=$?
        cd ../..

        # Report timing
        END_TIME=$(date +%s)
        DURATION=$((END_TIME - START_TIME))
        echo ""
        echo "=== Smoke Test Results ==="
        echo "Duration: ${DURATION}s"
        [ $TEST_EXIT -eq 0 ] && echo "Status: PASSED" || echo "Status: FAILED"
        exit $TEST_EXIT
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
        check_service "https://localhost:$AGENT_PORT/status" "Agent"
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
        echo "  test-smoke      Full E2E smoke tests with Playwright and real Claude API"
        echo "  test-release    Run full test suite: unit + integration + system tests"
        echo ""
        echo "Code quality:"
        echo "  lint            Run gofmt and staticcheck"
        echo "  check           Run lint + unit tests (pre-commit check)"
        echo ""
        echo "Deployment:"
        echo "  deploy-local                               Deploy locally (dev mode) with full test suite"
        echo "  deploy-local-quick                         Deploy locally (dev mode) - skips integration/system tests"
        echo "  stop-local                                 Stop local dev instance"
        echo "  quick-test                                 Fast iteration: build + deploy-quick + smoke tests"
        echo "  deploy-prod [host] [ssh-port] [ssh-key]    Deploy to remote (uses .env DEPLOY_* vars)"
        echo "  stop-prod [host] [ssh-port] [ssh-key]      Stop remote agency (uses .env DEPLOY_* vars)"
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
