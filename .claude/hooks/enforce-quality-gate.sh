#!/bin/sh
# enforce-quality-gate.sh - Runs on Stop/SubagentStop
# Validates code quality before session ends

set -e

cd "$CLAUDE_PROJECT_DIR" 2>/dev/null || exit 0

# Quick quality checks (non-blocking - failures are informational)
echo "Running quality gate checks..."

# Check go vet
if command -v go >/dev/null 2>&1; then
    if ! go vet ./... 2>&1; then
        echo "WARNING: go vet found issues"
    fi
fi

# Check golangci-lint
if command -v golangci-lint >/dev/null 2>&1; then
    if ! golangci-lint run --timeout 60s 2>&1; then
        echo "WARNING: golangci-lint found issues"
    fi
fi

# Check build
if command -v go >/dev/null 2>&1; then
    if ! go build ./... 2>&1; then
        echo "WARNING: build failed"
    fi
fi

exit 0
