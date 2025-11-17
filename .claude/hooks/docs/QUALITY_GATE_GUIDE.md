# Quality Gate Enforcement Guide

## Overview

This project implements a **strict quality gate system** that prevents Claude from completing tasks or marking todos as complete without achieving actual success. The system enforces:

1. **100% test success rate** - All tests must pass
2. **Zero linting errors** - All code must meet quality standards
3. **Valid workflows** - All GitHub Actions workflows must validate
4. **No false completions** - Claude cannot claim success without proving it

## How It Works

### Multi-Layer Enforcement

The quality gates are enforced at multiple hook points:

```
Code Change → PostToolUse hook → Immediate validation
     ↓
More Changes → PostToolUse hook → Immediate validation
     ↓
Task Completion Attempt → Stop/SubagentStop hook → GATE ENFORCEMENT
     ↓
Response Submission → UserPromptSubmit hook → Final check
     ↓
User sees result (only if all gates pass)
```

### Hook Execution Flow

#### 1. PostToolUse Hook (Immediate Feedback)
**File:** `post-code-change.sh`
- Triggers: After every `Edit` or `Write` on `.go` files
- Runs: Linting + tests on modified file/package
- Behavior: Provides immediate feedback but doesn't block (exit 1)
- Purpose: Early detection of issues

#### 2. Stop/SubagentStop Hooks (Quality Gate)
**File:** `enforce-quality-gate.sh` → `pre-completion-validator.sh`
- Triggers: When Claude tries to complete a task/response
- Runs: Full validation suite if code was modified
- Behavior: **BLOCKS completion** if any validation fails (exit 2)
- Purpose: Enforce quality gate before completion

#### 3. UserPromptSubmit Hook (Final Safety)
**File:** `pre-user-prompt-submit.sh`
- Triggers: Before sending response to user
- Runs: Quick validation on modified files
- Behavior: Blocks if validation fails (exit 1)
- Purpose: Final safety net

#### 4. PostToolUse on TodoWrite (Todo Protection)
**File:** `post-todo-update.sh` (if configured)
- Triggers: After TodoWrite tool usage
- Detects: When todos marked as `status: "completed"`
- Behavior: **BLOCKS todo update** if validation fails (exit 2)
- Purpose: Prevent false completion claims

## Validation Requirements

### Test Requirements
- **Command:** `make test-unit`
- **Requirement:** Exit code 0 (all tests pass)
- **Failure:** Any test failure blocks completion
- **Coverage:** Excludes integration tests for speed, focuses on unit tests

### Linting Requirements
- **Command:** `golangci-lint run --timeout 5m ./...`
- **Requirement:** Zero linting errors
- **Failure:** Any linting error blocks completion
- **Standards:** Uses project's `.golangci.yml` configuration

### Workflow Validation Requirements
- **Command:** `act --dryrun` or `act -l` on each workflow file
- **Requirement:** Valid YAML syntax and structure
- **Failure:** Invalid workflow syntax blocks completion
- **Scope:** All files in `.github/workflows/`

## Exit Codes and Behavior

### Exit Code 0 (Success)
- All validations passed
- Claude can proceed with completion
- Optional JSON output for additional control

### Exit Code 2 (Blocking Error)
- One or more validations failed
- Claude **CANNOT** complete the task
- Error message (stderr) is fed back to Claude
- Claude must fix issues and retry

### Exit Code 1 (Non-blocking Error)
- Used by PostToolUse hooks for feedback
- Shows error but doesn't block the tool call
- Claude sees the error and can take action

## Configuration

### Hook Configuration
Location: `.claude/settings.local.json`

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write",
        "hooks": [{
          "type": "command",
          "command": "$CLAUDE_PROJECT_DIR/.claude/hooks/post-code-change.sh",
          "timeout": 300
        }]
      }
    ],
    "Stop": [
      {
        "hooks": [{
          "type": "command",
          "command": "$CLAUDE_PROJECT_DIR/.claude/hooks/enforce-quality-gate.sh",
          "timeout": 600
        }]
      }
    ],
    "SubagentStop": [
      {
        "hooks": [{
          "type": "command",
          "command": "$CLAUDE_PROJECT_DIR/.claude/hooks/enforce-quality-gate.sh",
          "timeout": 600
        }]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [{
          "type": "command",
          "command": "$CLAUDE_PROJECT_DIR/.claude/hooks/pre-user-prompt-submit.sh",
          "timeout": 300
        }]
      }
    ]
  }
}
```

### Timeout Configuration
- **PostToolUse:** 300 seconds (5 minutes) - for quick validation
- **Stop/SubagentStop:** 600 seconds (10 minutes) - for full validation suite
- **UserPromptSubmit:** 300 seconds (5 minutes) - for final checks

## What Claude Sees When Blocked

### Successful Validation
```
============================================================
✅ ALL VALIDATIONS PASSED
============================================================

✓ Linting: PASSED
✓ Tests: PASSED (100% success rate)
✓ Workflows: VALIDATED

Task completion requirements satisfied.
============================================================
```

### Failed Validation (Blocks Completion)
```
============================================================
❌ VALIDATION FAILED - TASK CANNOT BE COMPLETED
============================================================

The following issues must be resolved before marking this task complete:

❌ LINTING FAILED: Code quality issues detected
❌ TESTS FAILED: Not all tests are passing

🔧 REQUIRED ACTIONS:
  1. Fix all linting issues: Run 'make lint' and address all warnings
  2. Fix all test failures: Run 'make test-unit' and ensure 100% pass rate
  3. Fix workflow errors: Validate .github/workflows/*.yml files
  4. Re-run validation after fixes

⚠️  YOU CANNOT CLAIM SUCCESS UNTIL ALL VALIDATIONS PASS
⚠️  YOU CANNOT MARK TODOS AS COMPLETE UNTIL ALL VALIDATIONS PASS
⚠️  YOU MUST FIX THE ERRORS BEFORE PROCEEDING
============================================================
```

## Common Scenarios

### Scenario 1: Clean Development Flow
1. Claude modifies code
2. PostToolUse hook validates immediately ✅
3. Tests pass, linting passes
4. Claude completes task
5. Stop hook validates ✅
6. All gates pass
7. Task marked complete ✅

### Scenario 2: Bug Introduced
1. Claude modifies code with a bug
2. PostToolUse hook runs tests ❌
3. Tests fail, Claude sees error immediately
4. Claude fixes the bug
5. PostToolUse hook validates ✅
6. Tests pass now
7. Claude completes task
8. Stop hook validates ✅
9. Task marked complete ✅

### Scenario 3: Premature Completion Attempt
1. Claude modifies code
2. PostToolUse shows test failures ❌
3. Claude thinks they're fixed (but they're not)
4. Claude tries to mark todo as "completed"
5. Stop hook runs full validation ❌
6. **BLOCKED** with exit code 2
7. Claude receives blocking error
8. Claude cannot complete task
9. Claude must fix actual issues
10. After fixes, validation passes ✅
11. Task can now be completed

### Scenario 4: Workflow Validation
1. Claude modifies `.github/workflows/test.yml`
2. Claude tries to complete task
3. Stop hook runs workflow validation
4. `act --dryrun` detects syntax error ❌
5. **BLOCKED** with exit code 2
6. Claude must fix workflow YAML
7. After fix, validation passes ✅
8. Task can be completed

## Testing the Quality Gates

### Manual Testing

Test individual hooks:
```bash
# Test post-code-change hook
.claude/hooks/post-code-change.sh Edit internal/usecase/video_service.go

# Test pre-completion validator
.claude/hooks/pre-completion-validator.sh

# Test quality gate enforcer
.claude/hooks/enforce-quality-gate.sh

# Test pre-submit hook
.claude/hooks/pre-user-prompt-submit.sh
```

### Simulated Failure Testing

Introduce a failing test:
```bash
# Create a failing test
echo 'package main; import "testing"; func TestFail(t *testing.T) { t.Fatal("fail") }' > /tmp/fail_test.go

# Try to complete (should be blocked)
.claude/hooks/pre-completion-validator.sh
# Expected: Exit code 2, error message about test failures
```

## Benefits

### For Users
- **Guaranteed Quality:** Code changes are validated before completion
- **No False Claims:** Claude cannot claim success without proving it
- **Fast Feedback:** Issues caught immediately during development
- **Confidence:** Completed tasks actually work

### For Claude
- **Clear Requirements:** Knows exactly what must pass
- **Immediate Feedback:** Errors shown right after code changes
- **Blocked if Failing:** Cannot complete until issues resolved
- **Guided Fixes:** Error messages suggest specific commands to run

### For Teams
- **Enforced Standards:** Quality gates apply to all Claude sessions
- **Consistent Quality:** Every change meets the same bar
- **Reduced Review Time:** Basic quality checks automated
- **Trust in Automation:** Claude's completions are reliable

## Customization

### Adjusting Validation Requirements

Edit `pre-completion-validator.sh`:

```bash
# Add coverage requirements
if ! make test-unit -cover | grep -q "coverage: 80%"; then
    VALIDATION_FAILED=1
    FAILURE_REASONS="${FAILURE_REASONS}\n❌ COVERAGE TOO LOW"
fi

# Add integration tests
if ! make test-integration; then
    VALIDATION_FAILED=1
    FAILURE_REASONS="${FAILURE_REASONS}\n❌ INTEGRATION TESTS FAILED"
fi

# Add security scanning
if ! gosec ./...; then
    VALIDATION_FAILED=1
    FAILURE_REASONS="${FAILURE_REASONS}\n❌ SECURITY ISSUES FOUND"
fi
```

### Adjusting Strictness

**Strict Mode (Current):** Exit code 2 blocks completion
**Lenient Mode:** Change exit code 2 to exit code 0 with warning

```bash
# In pre-completion-validator.sh, change:
exit 2

# To:
echo "⚠️ WARNING: Validations failed but not blocking"
exit 0
```

### Disabling Specific Checks

Comment out checks in `pre-completion-validator.sh`:

```bash
# Skip workflow validation
# if ! act --dryrun ...; then
#     VALIDATION_FAILED=1
# fi
```

## Troubleshooting

### Hook Not Running
- Check hook is executable: `ls -la .claude/hooks/*.sh`
- Check settings.local.json syntax is valid JSON
- Check hook path uses `$CLAUDE_PROJECT_DIR` variable
- Restart Claude Code to reload configuration

### False Positives
- Review validation output for specific errors
- Run validation commands manually: `make test-unit`, `make lint`
- Check if test environment is properly configured
- Verify act is installed: `which act`

### Timeouts
- Increase timeout in settings.local.json
- Optimize test suite for speed (use `-short` flag)
- Run fewer tests in validation (adjust Makefile)

### Validation Too Strict
- Adjust requirements in validation scripts
- Use lenient mode (exit 0 instead of exit 2)
- Disable specific checks temporarily
- Create separate validation profiles for different scenarios

## Advanced Usage

### Environment-Specific Validation

```bash
# In pre-completion-validator.sh
if [ "$CI" = "true" ]; then
    # CI mode: strict
    make test-unit
else
    # Local mode: fast
    make test-unit -short
fi
```

### Conditional Validation

```bash
# Only validate workflows if they changed
if git diff --name-only HEAD | grep -q '^.github/workflows/'; then
    echo "Workflows changed, validating..."
    # Run workflow validation
fi
```

### Custom Quality Metrics

```bash
# Check code complexity
if gocyclo -over 15 . | grep -q '.go'; then
    echo "❌ Code complexity too high"
    exit 2
fi

# Check dependency vulnerabilities
if govulncheck ./...; then
    echo "❌ Vulnerabilities found"
    exit 2
fi
```

## References

- [Claude Code Hooks Documentation](https://code.claude.com/docs/en/hooks)
- [golangci-lint Configuration](../../.golangci.yml)
- [Makefile Targets](../../Makefile)
- [GitHub Actions Workflows](../../.github/workflows/)
- [Act - Local GitHub Actions](https://github.com/nektos/act)
