# Cloud-Init Dependencies

Prerequisites for building, testing, and smoke testing the Agency project.

## Quick Install (Ubuntu 22.04/24.04)

```yaml
#cloud-config
package_update: true
packages:
  - git
  - curl
  - build-essential
  - ca-certificates

runcmd:
  # Go 1.24+
  - wget -q https://go.dev/dl/go1.24.0.linux-amd64.tar.gz -O /tmp/go.tar.gz
  - rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tar.gz
  - echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile.d/go.sh

  # Node.js 20 LTS (for smoke tests)
  - curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
  - apt-get install -y nodejs

  # Playwright system dependencies (for smoke tests)
  - npx playwright install-deps chromium

  # staticcheck (optional, for linting)
  - /usr/local/go/bin/go install honnef.co/go/tools/cmd/staticcheck@latest
```

## Dependencies by Test Level

| Level | Go | Node | Playwright | API Key |
|-------|-----|------|------------|---------|
| Build | Yes | No | No | No |
| Unit/Integration | Yes | No | No | No |
| System | Yes | No | No | No |
| Smoke | Yes | Yes | Yes | Yes |

## Manual Installation

### Build & Test (Unit/Integration/System)

```bash
# Ubuntu/Debian
apt-get update && apt-get install -y git curl build-essential

# Go 1.24+
wget https://go.dev/dl/go1.24.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.24.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin

# Verify
go version  # should show go1.24.0+
```

### Smoke Tests (additional)

```bash
# Node.js 20 LTS
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt-get install -y nodejs

# Playwright browsers + system deps
cd tests/smoke
npm ci
npx playwright install --with-deps chromium

# Environment variables required
export ANTHROPIC_API_KEY="your-key"
export AG_WEB_PASSWORD="your-password"  # optional, defaults to 'smoketest'
```

### Optional: Linting

```bash
go install honnef.co/go/tools/cmd/staticcheck@latest
```

## Verification

```bash
# Build
./build.sh build

# Unit tests
./build.sh test

# Integration tests
./build.sh test-int

# System tests
./build.sh test-sys

# Smoke tests (requires ANTHROPIC_API_KEY)
./build.sh test-smoke
```
