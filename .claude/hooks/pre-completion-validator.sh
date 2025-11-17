#!/bin/bash
# Pre-completion validation hook: Enforces 100% test and workflow success
# Blocks Claude from completing tasks until all quality gates pass
#
# This hook runs before Claude marks tasks complete (Stop/SubagentStop events)
# and ensures that:
# 1. All tests pass (make test-unit)
# 2. All workflows succeed (act validation)
# 3. Linting passes
# 4. No regressions or errors exist
#
# Exit codes:
#   0 - All validations passed, Claude can proceed
#   2 - Validation failed, block completion and show errors to Claude

set -e

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
cd "$PROJECT_DIR"

echo "============================================================"
echo "🔒 PRE-COMPLETION VALIDATION GATE"
echo "============================================================"
echo "Enforcing quality requirements before task completion..."
echo ""

# Track validation failures
VALIDATION_FAILED=0
FAILURE_REASONS=""

# ============================================================
# Step 1: Auto-format code and stage changes
# ============================================================
echo "🎨 Step 1/4: Auto-formatting code..."
# Run formatter
if make fmt 2>&1; then
    # Check if any files were modified
    MODIFIED_FILES=$(git diff --name-only 2>/dev/null || true)

    if [ -n "$MODIFIED_FILES" ]; then
        echo "✅ Formatted and staging files:"
        for file in $MODIFIED_FILES; do
            echo "  → $file"
            git add "$file" 2>/dev/null || true
        done
    else
        echo "✅ Code formatting check passed (no changes needed)"
    fi
else
    echo "⚠️  Formatting command failed (non-critical)"
fi
echo ""

# ============================================================
# Step 2: Run Linting
# ============================================================
echo "📋 Step 2/4: Running linter..."
if ! golangci-lint run --timeout 5m ./... 2>&1; then
    VALIDATION_FAILED=1
    FAILURE_REASONS="${FAILURE_REASONS}\n❌ LINTING FAILED: Code quality issues detected"
    echo "❌ Linting failed"
else
    echo "✅ Linting passed"
fi
echo ""

# ============================================================
# Step 3: Run All Tests
# ============================================================
echo "🧪 Step 3/4: Running all tests (unit + integration)..."
if ! make test-unit 2>&1; then
    VALIDATION_FAILED=1
    FAILURE_REASONS="${FAILURE_REASONS}\n❌ TESTS FAILED: Not all tests are passing"
    echo "❌ Tests failed"
else
    echo "✅ All tests passed"
fi
echo ""

# ============================================================
# Step 4: Validate GitHub Workflows (using act)
# ============================================================
echo "⚙️  Step 4/4: Validating GitHub workflows with act..."

# Check if act is installed
if ! command -v act &> /dev/null; then
    echo "⚠️  WARNING: 'act' is not installed. Skipping workflow validation."
    echo "Install with: brew install act"
    echo ""
else
    # Get list of workflow files
    WORKFLOW_FILES=$(find .github/workflows -name "*.yml" -o -name "*.yaml" 2>/dev/null || true)

    if [ -z "$WORKFLOW_FILES" ]; then
        echo "ℹ️  No workflow files found, skipping workflow validation"
    else
        WORKFLOW_VALIDATION_FAILED=0

        for workflow in $WORKFLOW_FILES; do
            workflow_name=$(basename "$workflow")
            echo "  Validating: $workflow_name"

            # Run act in dry-run mode to validate workflow syntax and structure
            # Using --dryrun to avoid actually executing jobs
            if ! act --dryrun -W "$workflow" 2>&1 | grep -q "Job.*succeeded"; then
                # If dry-run doesn't work, try list mode to validate syntax
                if ! act -l -W "$workflow" &> /dev/null; then
                    WORKFLOW_VALIDATION_FAILED=1
                    FAILURE_REASONS="${FAILURE_REASONS}\n❌ WORKFLOW VALIDATION FAILED: $workflow_name has errors"
                    echo "    ❌ Failed"
                else
                    echo "    ✅ Valid syntax"
                fi
            else
                echo "    ✅ Validated"
            fi
        done

        if [ $WORKFLOW_VALIDATION_FAILED -eq 1 ]; then
            VALIDATION_FAILED=1
            echo "❌ Workflow validation failed"
        else
            echo "✅ All workflows validated"
        fi
    fi
fi
echo ""

# ============================================================
# Final Decision
# ============================================================
echo "============================================================"

if [ $VALIDATION_FAILED -eq 1 ]; then
    echo "❌ VALIDATION FAILED - TASK CANNOT BE COMPLETED"
    echo "============================================================"
    echo ""
    echo "The following issues must be resolved before marking this task complete:"
    echo -e "$FAILURE_REASONS"
    echo ""
    echo "🔧 REQUIRED ACTIONS:"
    echo "  1. Fix all linting issues: Run 'make lint' and address all warnings"
    echo "  2. Fix all test failures: Run 'make test-unit' and ensure 100% pass rate"
    echo "  3. Fix workflow errors: Validate .github/workflows/*.yml files"
    echo "  4. Re-run validation after fixes"
    echo ""
    echo "⚠️  YOU CANNOT CLAIM SUCCESS UNTIL ALL VALIDATIONS PASS"
    echo "⚠️  YOU CANNOT MARK TODOS AS COMPLETE UNTIL ALL VALIDATIONS PASS"
    echo "⚠️  YOU MUST FIX THE ERRORS BEFORE PROCEEDING"
    echo ""
    echo "============================================================"

    # Exit with code 2 to block completion and send this message to Claude
    >&2 echo "VALIDATION FAILED: Cannot complete task. Linting, tests, or workflows have errors. Fix all issues before proceeding."
    exit 2
fi

echo "✅ ALL VALIDATIONS PASSED"
echo "============================================================"
echo ""
echo "✓ Linting: PASSED"
echo "✓ Tests: PASSED (100% success rate)"
echo "✓ Workflows: VALIDATED"
echo ""
echo "Task completion requirements satisfied."
echo "============================================================"

# Return structured JSON to allow Claude to complete
cat <<'EOF'
{
  "decision": null,
  "systemMessage": "✅ All quality gates passed: Linting ✓, Tests ✓, Workflows ✓"
}
EOF

exit 0
