# Release Process

How to prepare and publish releases for Agency.

**Related:** [AGENTS.md](../AGENTS.md) (quick reference), [CHANGELOG.md](../CHANGELOG.md)

---

## Steps

```bash
# 1. Run all checks
./build.sh prepare-release

# 2. Update CHANGELOG.md (add: ## [X.Y.Z] - YYYY-MM-DD)

# 3. Review docs for completed TODOs

# 4. Create release
./build.sh release X.Y.Z

# 5. Push
git push origin main vX.Y.Z
```

---

## What the Commands Do

**`prepare-release`** runs:
- `check` (format + lint + test)
- `test-all` (unit + integration)
- `test-sys` (system tests)
- Local deployment test
- Shows git log since last tag

**`release X.Y.Z`**:
- Validates semver format
- Checks CHANGELOG.md has entry for version
- Creates commit and tag

---

## Versioning

Follow [semver](https://semver.org/):
- **Major** (X.0.0): Breaking API changes
- **Minor** (0.X.0): New features, backward compatible
- **Patch** (0.0.X): Bug fixes only

Git-derived version format:
- `v1.2.3` - Clean tag
- `v1.2.3-5-g1a2b3c4` - 5 commits after tag
- `v1.2.3-5-g1a2b3c4-dirty` - With uncommitted changes
