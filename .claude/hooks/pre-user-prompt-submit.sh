#!/bin/bash
# Pre-user-prompt-submit hook: Runs before Claude sends a response to the user
# Ensures overall codebase quality when Go code has been modified

set -e

# Check if any Go files were modified in this session
# This is a safety check before sending responses to the user

echo "🔍 Pre-submit validation: Checking overall codebase health..."

# Quick linter check on recently modified files (if any)
# This is a lighter check than full CI
MODIFIED_FILES=$(git diff --name-only --diff-filter=ACMR 2>/dev/null | grep '\.go$' || true)

if [ -z "$MODIFIED_FILES" ]; then
    echo "ℹ️  No Go files modified in this session"
    exit 0
fi

echo "📋 Running quick linter check on modified files..."
for file in $MODIFIED_FILES; do
    if [ -f "$file" ]; then
        echo "  Checking: $file"
        if ! golangci-lint run "$file" --timeout 2m --fast; then
            echo "❌ BLOCKED: Linter issues detected in $file"
            echo "💡 FIX: Use go-backend-reviewer agent to resolve code quality issues"
            exit 1
        fi
    fi
done

# Run quick test suite on affected packages
echo "🧪 Running tests on modified packages..."
AFFECTED_PACKAGES=$(echo "$MODIFIED_FILES" | xargs -I{} dirname {} | sort -u)

for pkg in $AFFECTED_PACKAGES; do
    if [ -d "$pkg" ]; then
        echo "  Testing: $pkg"
        if ! go test -timeout 30s "./$pkg" -short; then
            echo "❌ BLOCKED: Tests failed in $pkg"
            echo "💡 FIX: Use golang-test-guardian agent to validate and fix business logic"
            exit 1
        fi
    fi
done

# Check if critical files were modified
CRITICAL_FILES=$(echo "$MODIFIED_FILES" | grep -E '(usecase|repository|domain)/' || true)
if [ -n "$CRITICAL_FILES" ]; then
    echo "⚠️  WARNING: Critical business logic files modified:"
    echo "$CRITICAL_FILES"
    echo "💡 RECOMMENDATION: Ensure golang-test-guardian agent was used to validate changes"
fi

echo "✅ Pre-submit validation passed - codebase is healthy"
exit 0
