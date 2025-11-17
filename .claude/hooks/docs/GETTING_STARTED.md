# Getting Started with Quality Gates

## Quick Start (5 Minutes)

### 1. Verify Installation

Run the test script to ensure everything is configured:

```bash
./.claude/hooks/test-hooks.sh
```

Expected output:
```
✅ ALL TESTS PASSED

Quality gate hooks are properly configured and functional.
Claude will be unable to complete tasks without:
  - 100% test success rate
  - Zero linting errors
  - Valid workflow files
```

### 2. Test the System

Try making a change that breaks tests:

```bash
# Create a failing test (temporary)
cat > /tmp/test_fail.go <<'EOF'
package main
import "testing"
func TestFail(t *testing.T) { t.Fatal("intentional failure") }
EOF

# Copy to project
cp /tmp/test_fail.go ./internal/test_fail_test.go

# Try to run validation
./.claude/hooks/pre-completion-validator.sh
```

You should see:
```
❌ VALIDATION FAILED - TASK CANNOT BE COMPLETED
...
⚠️  YOU CANNOT CLAIM SUCCESS UNTIL ALL VALIDATIONS PASS
```

Remove the test file:
```bash
rm ./internal/test_fail_test.go
```

### 3. Verify Configuration

Check that hooks are configured:

```bash
jq '.hooks' ./.claude/settings.local.json
```

You should see all four hook events configured: `PostToolUse`, `Stop`, `SubagentStop`, `UserPromptSubmit`.

## What Happens Now

### When Claude Makes Code Changes

1. **Immediate Feedback** (PostToolUse hook)
   - Runs after every code edit
   - Shows linting/test errors immediately
   - Provides quick feedback cycle

2. **Completion Enforcement** (Stop/SubagentStop hooks)
   - Runs when Claude tries to finish
   - **BLOCKS** completion if validation fails
   - Requires 100% test success to proceed

3. **Final Safety Check** (UserPromptSubmit hook)
   - Runs before sending response to you
   - Last chance to catch issues
   - Ensures quality before you see results

### Example Workflow

```
You: "Add a new feature to video service"

Claude: Makes code changes
  ↓
PostToolUse Hook: Validates changes immediately
  ✅ Linting passes
  ✅ Tests pass
  ↓
Claude: Continues working
  ↓
Claude: Tries to complete task
  ↓
Stop Hook: Runs full validation
  ✅ All linting passes (100%)
  ✅ All tests pass (100%)
  ✅ All workflows valid
  ↓
UserPromptSubmit Hook: Final check
  ✅ All modified files validated
  ↓
You: See completed task with confidence
```

### If Validation Fails

```
You: "Add a new feature"

Claude: Makes code changes
  ↓
PostToolUse Hook: Tests fail ❌
  Shows error to Claude
  ↓
Claude: Attempts to fix
  ↓
Claude: Tries to complete task
  ↓
Stop Hook: Runs validation
  ❌ Tests still failing
  ❌ BLOCKED with exit code 2
  ↓
Claude sees:
  "VALIDATION FAILED: Cannot complete task.
   Fix all issues before proceeding."
  ↓
Claude: CANNOT complete task
Claude: CANNOT mark todos as "completed"
Claude: MUST fix the errors
  ↓
Claude: Makes proper fix
  ↓
Claude: Tries completion again
  ↓
Stop Hook: Validates
  ✅ All tests pass now
  ✅ Completion allowed
  ↓
You: See completed task
```

## Key Files

### Hook Scripts (Executable)
- **`pre-completion-validator.sh`** - Main validation engine
- **`enforce-quality-gate.sh`** - Smart gate enforcer
- **`post-code-change.sh`** - Immediate feedback hook
- **`pre-user-prompt-submit.sh`** - Final safety check
- **`post-todo-update.sh`** - Todo completion prevention
- **`test-hooks.sh`** - Test suite for validation

### Documentation
- **`README.md`** - Overview and hook descriptions
- **`QUALITY_GATE_GUIDE.md`** - Detailed implementation guide
- **`QUICK_REFERENCE.md`** - Quick reference card
- **`ARCHITECTURE.md`** - System architecture diagrams
- **`IMPLEMENTATION_SUMMARY.md`** - Implementation details
- **`GETTING_STARTED.md`** - This file

### Configuration
- **`.claude/settings.local.json`** - Hook configuration

## Common Commands

```bash
# Test all hooks
./.claude/hooks/test-hooks.sh

# Test validation manually
./.claude/hooks/pre-completion-validator.sh

# Test quality gate enforcer
./.claude/hooks/enforce-quality-gate.sh

# Run tests
make test-unit

# Run linting
make lint

# Validate workflow files
act --dryrun -W .github/workflows/test.yml

# Check hook configuration
jq '.hooks' .claude/settings.local.json

# Verify hook permissions
ls -la .claude/hooks/*.sh
```

## What Claude Sees

### Success Message
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

### Failure Message (Blocks Completion)
```
============================================================
❌ VALIDATION FAILED - TASK CANNOT BE COMPLETED
============================================================

The following issues must be resolved before marking this task complete:

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

## Understanding Exit Codes

| Exit Code | Meaning | Claude's Response |
|-----------|---------|-------------------|
| 0 | Success, all validations passed | Continues normally, can complete task |
| 1 | Non-blocking error | Sees error message, can continue but aware of issue |
| 2 | Blocking error | **CANNOT continue**, must fix errors, cannot complete |

## Troubleshooting

### Hooks Not Running
```bash
# Check permissions
ls -la .claude/hooks/*.sh

# If not executable, fix:
chmod +x .claude/hooks/*.sh
```

### Configuration Issues
```bash
# Validate JSON syntax
jq . .claude/settings.local.json

# If error, fix the JSON syntax
```

### Missing Dependencies
```bash
# Check what's installed
which golangci-lint go act jq

# Install missing tools
brew install golangci-lint act jq
```

### Tests Timing Out
```bash
# Edit settings.local.json
# Increase timeout values:
# - PostToolUse: from 300 to 600
# - Stop/SubagentStop: from 600 to 1200
```

### False Positives
```bash
# Run validation manually to debug
./.claude/hooks/pre-completion-validator.sh

# Check specific issues
make test-unit     # See which tests fail
make lint          # See which linting errors
act -l             # List workflows
```

## Customization

### Adjust Validation Requirements

Edit `.claude/hooks/pre-completion-validator.sh`:

```bash
# Add coverage requirement
if ! make test-unit -cover | grep -q "coverage: 80%"; then
    VALIDATION_FAILED=1
fi

# Add security scanning
if ! gosec ./...; then
    VALIDATION_FAILED=1
fi

# Skip workflow validation
# Comment out the workflow validation section
```

### Adjust Timeouts

Edit `.claude/settings.local.json`:

```json
{
  "hooks": {
    "Stop": [{
      "hooks": [{
        "timeout": 1200  // Change from 600 to 1200 (20 minutes)
      }]
    }]
  }
}
```

### Disable Temporarily

```bash
# Backup configuration
cp .claude/settings.local.json .claude/settings.local.json.backup

# Remove hooks section
jq 'del(.hooks)' .claude/settings.local.json > /tmp/settings.json
mv /tmp/settings.json .claude/settings.local.json

# To restore
mv .claude/settings.local.json.backup .claude/settings.local.json
```

**WARNING:** Only disable for debugging. Re-enable immediately.

## Best Practices

### For Daily Development

1. **Let the hooks work** - Don't disable them
2. **Read error messages** - They provide fix instructions
3. **Run tests locally** - Before asking Claude to complete
4. **Use verbose mode** - Ctrl+O in Claude Code to see hook output
5. **Keep hooks updated** - As project requirements evolve

### For Claude

1. **Always run tests** after code changes
2. **Never claim completion** without validation passing
3. **Never mark todos complete** until all gates pass
4. **Read hook error messages** carefully
5. **Fix all issues** before attempting completion again

### For Teams

1. **Commit hook scripts** - Share quality gates across team
2. **Keep settings.local.json local** - Don't commit (in .gitignore)
3. **Document custom requirements** - If you add validations
4. **Update timeouts as needed** - Based on project size
5. **Review hook output** - In CI/CD for patterns

## Next Steps

1. ✅ Run `./.claude/hooks/test-hooks.sh` to verify setup
2. ✅ Review `QUICK_REFERENCE.md` for command reference
3. ✅ Read `QUALITY_GATE_GUIDE.md` for detailed documentation
4. ✅ Check `ARCHITECTURE.md` for system design understanding
5. ✅ Start using Claude - quality gates are active!

## Support

### Documentation Files
- Overview: `README.md`
- Quick Ref: `QUICK_REFERENCE.md`
- Full Guide: `QUALITY_GATE_GUIDE.md`
- Architecture: `ARCHITECTURE.md`
- Implementation: `IMPLEMENTATION_SUMMARY.md`

### Testing
```bash
# Full test suite
./.claude/hooks/test-hooks.sh

# Individual validation
./.claude/hooks/pre-completion-validator.sh
```

### Debugging
```bash
# Enable verbose mode in Claude Code (Ctrl+O)
# Run validation manually to see exact errors
# Check hook output for specific issues
```

## Success Indicators

You'll know the system is working when:
- ✅ Claude cannot complete tasks with failing tests
- ✅ Claude cannot mark todos complete with errors
- ✅ Claude receives blocking error messages (exit 2)
- ✅ Claude must fix issues before proceeding
- ✅ All tasks that complete have 100% test success

## Welcome Message

```
🎉 Quality Gate System Active!

From now on, Claude cannot complete tasks or mark todos as "completed"
without achieving:
  ✅ 100% test success rate
  ✅ Zero linting errors
  ✅ Valid workflow files

This ensures every completed task actually works.

Run `./.claude/hooks/test-hooks.sh` to verify setup.
Read `QUICK_REFERENCE.md` for common commands.

Happy coding with confidence! 🚀
```
