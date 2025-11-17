#!/bin/bash
# Test script to validate hook configuration and functionality
# This script tests all quality gate hooks without modifying the codebase

set -e

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
cd "$PROJECT_DIR"

echo "============================================================"
echo "🧪 TESTING CLAUDE CODE QUALITY GATE HOOKS"
echo "============================================================"
echo ""

# Track test results
TESTS_PASSED=0
TESTS_FAILED=0

# Function to run a test
run_test() {
    local test_name="$1"
    local command="$2"
    local expected_exit_code="${3:-0}"

    echo "Testing: $test_name"

    if eval "$command" >/dev/null 2>&1; then
        actual_exit_code=0
    else
        actual_exit_code=$?
    fi

    if [ "$actual_exit_code" -eq "$expected_exit_code" ]; then
        echo "  ✅ PASS (exit code: $actual_exit_code)"
        TESTS_PASSED=$((TESTS_PASSED + 1))
    else
        echo "  ❌ FAIL (expected: $expected_exit_code, got: $actual_exit_code)"
        TESTS_FAILED=$((TESTS_FAILED + 1))
    fi
    echo ""
}

# Function to check file exists and is executable
check_file() {
    local file="$1"
    local description="$2"

    echo "Checking: $description"

    if [ ! -f "$file" ]; then
        echo "  ❌ FAIL - File does not exist: $file"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi

    if [ ! -x "$file" ]; then
        echo "  ⚠️  WARNING - File is not executable: $file"
        echo "  Fixing permissions..."
        chmod +x "$file"
    fi

    echo "  ✅ PASS - File exists and is executable"
    TESTS_PASSED=$((TESTS_PASSED + 1))
    echo ""
}

# Function to check JSON validity
check_json() {
    local file="$1"
    local description="$2"

    echo "Checking: $description"

    if [ ! -f "$file" ]; then
        echo "  ❌ FAIL - File does not exist: $file"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi

    if ! jq empty "$file" 2>/dev/null; then
        echo "  ❌ FAIL - Invalid JSON syntax"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        return 1
    fi

    echo "  ✅ PASS - Valid JSON"
    TESTS_PASSED=$((TESTS_PASSED + 1))
    echo ""
}

echo "============================================================"
echo "PHASE 1: File Existence and Permissions"
echo "============================================================"
echo ""

check_file "$PROJECT_DIR/.claude/hooks/post-code-change.sh" "Post-code-change hook"
check_file "$PROJECT_DIR/.claude/hooks/pre-completion-validator.sh" "Pre-completion validator hook"
check_file "$PROJECT_DIR/.claude/hooks/enforce-quality-gate.sh" "Quality gate enforcer hook"
check_file "$PROJECT_DIR/.claude/hooks/pre-user-prompt-submit.sh" "Pre-user-prompt-submit hook"

echo "============================================================"
echo "PHASE 2: Configuration Validation"
echo "============================================================"
echo ""

check_json "$PROJECT_DIR/.claude/settings.local.json" "Settings configuration JSON"

# Check if hooks are configured
echo "Checking: Hook configuration in settings.local.json"
if jq -e '.hooks' "$PROJECT_DIR/.claude/settings.local.json" >/dev/null 2>&1; then
    echo "  ✅ PASS - Hooks section found"
    TESTS_PASSED=$((TESTS_PASSED + 1))

    # Check specific hooks
    for hook_type in "PostToolUse" "Stop" "SubagentStop" "UserPromptSubmit"; do
        if jq -e ".hooks.$hook_type" "$PROJECT_DIR/.claude/settings.local.json" >/dev/null 2>&1; then
            echo "  ✅ $hook_type configured"
        else
            echo "  ⚠️  $hook_type not configured"
        fi
    done
else
    echo "  ❌ FAIL - No hooks section found"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi
echo ""

echo "============================================================"
echo "PHASE 3: Hook Functionality Tests"
echo "============================================================"
echo ""

# Test that pre-completion validator can run
echo "Testing: Pre-completion validator execution"
if [ -f "$PROJECT_DIR/.claude/hooks/pre-completion-validator.sh" ]; then
    # Create a temporary marker to check if we're in clean state
    if git diff --quiet HEAD 2>/dev/null; then
        echo "  ℹ️  No changes detected (clean git state)"
        echo "  Running validation in clean state..."

        if "$PROJECT_DIR/.claude/hooks/pre-completion-validator.sh" >/dev/null 2>&1; then
            echo "  ✅ PASS - Validator runs successfully in clean state"
            TESTS_PASSED=$((TESTS_PASSED + 1))
        else
            exit_code=$?
            if [ $exit_code -eq 2 ]; then
                echo "  ⚠️  BLOCKED - Validation failed (exit 2)"
                echo "  This means there are existing quality issues to fix"
                echo "  Run: .claude/hooks/pre-completion-validator.sh"
            else
                echo "  ❌ FAIL - Unexpected exit code: $exit_code"
                TESTS_FAILED=$((TESTS_FAILED + 1))
            fi
        fi
    else
        echo "  ℹ️  Changes detected in working directory"
        echo "  Skipping validation test (would reflect current state)"
    fi
else
    echo "  ❌ FAIL - Validator script not found"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi
echo ""

# Test quality gate enforcer
echo "Testing: Quality gate enforcer execution"
if [ -f "$PROJECT_DIR/.claude/hooks/enforce-quality-gate.sh" ]; then
    if "$PROJECT_DIR/.claude/hooks/enforce-quality-gate.sh" >/dev/null 2>&1; then
        echo "  ✅ PASS - Enforcer runs successfully"
        TESTS_PASSED=$((TESTS_PASSED + 1))
    else
        exit_code=$?
        if [ $exit_code -eq 2 ]; then
            echo "  ⚠️  BLOCKED - Quality gate enforcement active (exit 2)"
            echo "  This means validation would block completion"
        else
            echo "  ❌ FAIL - Unexpected exit code: $exit_code"
            TESTS_FAILED=$((TESTS_FAILED + 1))
        fi
    fi
else
    echo "  ❌ FAIL - Enforcer script not found"
    TESTS_FAILED=$((TESTS_FAILED + 1))
fi
echo ""

echo "============================================================"
echo "PHASE 4: Dependency Checks"
echo "============================================================"
echo ""

# Check required tools
echo "Checking: Required tool dependencies"

tools_ok=true

if command -v golangci-lint >/dev/null 2>&1; then
    echo "  ✅ golangci-lint installed"
else
    echo "  ❌ golangci-lint NOT installed"
    echo "     Install: brew install golangci-lint"
    tools_ok=false
fi

if command -v go >/dev/null 2>&1; then
    echo "  ✅ go installed ($(go version))"
else
    echo "  ❌ go NOT installed"
    tools_ok=false
fi

if command -v act >/dev/null 2>&1; then
    echo "  ✅ act installed (for workflow validation)"
else
    echo "  ⚠️  act NOT installed (workflow validation will be skipped)"
    echo "     Install: brew install act"
fi

if command -v jq >/dev/null 2>&1; then
    echo "  ✅ jq installed"
else
    echo "  ⚠️  jq NOT installed (used for JSON manipulation)"
    echo "     Install: brew install jq"
fi

if [ "$tools_ok" = true ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    echo ""
    echo "  ✅ PASS - All required tools installed"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    echo ""
    echo "  ❌ FAIL - Some required tools missing"
fi
echo ""

echo "============================================================"
echo "PHASE 5: Integration Tests"
echo "============================================================"
echo ""

# Test that Makefile targets exist
echo "Checking: Makefile targets"

targets_ok=true

for target in "test-unit" "lint"; do
    if grep -q "^${target}:" Makefile 2>/dev/null; then
        echo "  ✅ make $target - target exists"
    else
        echo "  ❌ make $target - target NOT found"
        targets_ok=false
    fi
done

if [ "$targets_ok" = true ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    echo ""
    echo "  ✅ PASS - All required Makefile targets exist"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    echo ""
    echo "  ❌ FAIL - Some Makefile targets missing"
fi
echo ""

# Test GitHub workflows exist
echo "Checking: GitHub workflow files"

if [ -d ".github/workflows" ]; then
    workflow_count=$(find .github/workflows -name "*.yml" -o -name "*.yaml" 2>/dev/null | wc -l)
    echo "  ✅ Found $workflow_count workflow file(s)"
    TESTS_PASSED=$((TESTS_PASSED + 1))
else
    echo "  ⚠️  No .github/workflows directory"
    echo "  Workflow validation will be skipped"
fi
echo ""

echo "============================================================"
echo "TEST SUMMARY"
echo "============================================================"
echo ""
echo "Tests Passed: $TESTS_PASSED"
echo "Tests Failed: $TESTS_FAILED"
echo ""

if [ $TESTS_FAILED -eq 0 ]; then
    echo "✅ ALL TESTS PASSED"
    echo ""
    echo "Quality gate hooks are properly configured and functional."
    echo "Claude will be unable to complete tasks without:"
    echo "  - 100% test success rate"
    echo "  - Zero linting errors"
    echo "  - Valid workflow files"
    echo ""
    exit 0
else
    echo "❌ SOME TESTS FAILED"
    echo ""
    echo "Please review the failures above and fix the issues."
    echo "Common fixes:"
    echo "  - chmod +x .claude/hooks/*.sh"
    echo "  - brew install golangci-lint act jq"
    echo "  - Validate .claude/settings.local.json JSON syntax"
    echo ""
    exit 1
fi
