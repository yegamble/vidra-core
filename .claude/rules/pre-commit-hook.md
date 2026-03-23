## Pre-Commit Hook

The `.githooks/pre-commit` hook runs on every commit. Configure with env vars:

| Env Var | Default | Purpose |
|---------|---------|---------|
| `PRECOMMIT_SKIP_HOOKS` | `go-unit-tests,test-coverage` | Comma-separated hooks to skip |
| `LINT_TIMEOUT_SECONDS` | `180` | Lint step timeout (0 = unlimited) |
| `HOOK_TIMEOUT_SECONDS` | `240` | Per-hook timeout (0 = unlimited) |

### Common Overrides

```bash
# Run everything (slowest, use before PR)
PRECOMMIT_SKIP_HOOKS="" git commit -m "..."

# Skip all slow hooks (fastest)
PRECOMMIT_SKIP_HOOKS="go-unit-tests,test-coverage,lint" git commit -m "..."

# Unlimited timeout for slow machines
LINT_TIMEOUT_SECONDS=0 git commit -m "..."
```

### What the Hook Runs

1. `gofmt` — formats staged Go files and re-stages them
2. Linting via golangci-lint (skippable via `PRECOMMIT_SKIP_HOOKS`)
3. Optional hooks (go-unit-tests, test-coverage) — skipped by default

### Setup

```bash
make install-hooks   # Configure git to use .githooks/
```

Without this, the hook won't run. New developers must run this once.
