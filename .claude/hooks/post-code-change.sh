#!/bin/bash
# Post-code-change hook: Runs after Edit/Write tool calls on Go files
# Ensures code quality, tests pass, and business logic is maintained

set -e

# Extract tool name and file path from arguments
TOOL_NAME="${1:-unknown}"
FILE_PATH="${2:-}"

# Only run for Go files that were edited or written
if [[ "$FILE_PATH" != *.go ]]; then
    exit 0
fi

echo "🔍 Post-code-change hook triggered for: $FILE_PATH"

# Determine the package directory
PACKAGE_DIR=$(dirname "$FILE_PATH")

# Run linter on the modified file
echo "📋 Running linter..."
if ! golangci-lint run "$FILE_PATH" --timeout 5m; then
    echo "❌ FAILED: Linter found issues in $FILE_PATH"
    echo "💡 TIP: Use go-backend-reviewer agent to fix code quality issues"
    exit 1
fi

# Run tests for the affected package
echo "🧪 Running tests for package: $PACKAGE_DIR"
if ! go test -timeout 30s "$PACKAGE_DIR" -v; then
    echo "❌ FAILED: Tests failed in $PACKAGE_DIR"
    echo "💡 TIP: Use golang-test-guardian agent to validate business logic"
    exit 1
fi

# If this is a test file that was modified, extra validation
if [[ "$FILE_PATH" == *_test.go ]]; then
    echo "🔬 Test file modified - validating business logic preservation..."
    echo "💡 RECOMMENDATION: Run golang-test-guardian agent to ensure business logic is intact"
fi

# If this is production code in usecase/repository/domain, validate thoroughly
if [[ "$FILE_PATH" == */usecase/* ]] || [[ "$FILE_PATH" == */repository/* ]] || [[ "$FILE_PATH" == */domain/* ]]; then
    echo "⚠️  Critical business logic file modified: $FILE_PATH"
    echo "💡 RECOMMENDATION: Run golang-test-guardian agent to validate API contracts"
    echo "💡 RECOMMENDATION: Run go-backend-reviewer agent for architecture compliance"
fi

echo "✅ Post-code-change validation passed for: $FILE_PATH"
exit 0
