#!/bin/bash
# Post-TodoWrite validation hook: Enforces quality gates when todos are marked complete
#
# This hook runs after Claude uses the TodoWrite tool and checks if any todos
# were marked as "completed". If so, it validates that all quality requirements
# are met before allowing the completion.
#
# Exit codes:
#   0 - Validation passed or no todos completed
#   2 - Todos marked complete but validation failed - BLOCKS completion

set -e

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
cd "$PROJECT_DIR"

# Extract tool input to check if any todos were marked as completed
# The tool input is passed as environment variables or we parse from stdin
TOOL_INPUT="${1:-}"

# Check if this is a TodoWrite that marks something as completed
# We need to parse the tool's JSON input to see if status:"completed" appears
if echo "$TOOL_INPUT" | grep -q '"status"[[:space:]]*:[[:space:]]*"completed"'; then
    echo "🔍 Detected todo(s) marked as COMPLETED - triggering validation..."
    echo ""

    # Run the pre-completion validator
    if ! /Users/yosefgamble/github/athena/.claude/hooks/pre-completion-validator.sh; then
        echo ""
        echo "❌ BLOCKED: Cannot mark todos as complete while validations fail"
        echo ""
        >&2 echo "BLOCKED: You marked todos as 'completed' but tests/linting/workflows are failing. Fix all errors before marking tasks complete."
        exit 2
    fi

    echo "✅ Validation passed - todo completion allowed"
    exit 0
fi

# No todos marked as completed, allow the operation
exit 0
