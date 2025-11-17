#!/bin/bash
# Lightweight quality gate enforcer
# This runs on Stop/SubagentStop and checks if any code was modified
# If code was modified, it enforces validation before allowing completion
#
# Exit codes:
#   0 - No code changes or validation passed
#   2 - Code changed but validation failed - BLOCKS completion

set -e

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
cd "$PROJECT_DIR"

# Check if any Go files were modified
MODIFIED_GO_FILES=$(git diff --name-only HEAD 2>/dev/null | grep '\.go$' || true)

# Check if any workflow files were modified
MODIFIED_WORKFLOWS=$(git diff --name-only HEAD 2>/dev/null | grep '^\.github/workflows/' || true)

# If no relevant files modified, allow completion
if [ -z "$MODIFIED_GO_FILES" ] && [ -z "$MODIFIED_WORKFLOWS" ]; then
    echo "ℹ️  No code or workflow changes detected - skipping validation"
    exit 0
fi

echo "🔍 Code or workflow changes detected - enforcing quality gate..."
echo ""

if [ -n "$MODIFIED_GO_FILES" ]; then
    echo "Modified Go files:"
    echo "$MODIFIED_GO_FILES" | sed 's/^/  - /'
    echo ""
fi

if [ -n "$MODIFIED_WORKFLOWS" ]; then
    echo "Modified workflows:"
    echo "$MODIFIED_WORKFLOWS" | sed 's/^/  - /'
    echo ""
fi

# Run full validation
exec /Users/yosefgamble/github/athena/.claude/hooks/pre-completion-validator.sh
