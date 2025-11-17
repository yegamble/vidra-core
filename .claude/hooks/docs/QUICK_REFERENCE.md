# Quality Gate Quick Reference

## TL;DR

Claude **CANNOT** complete tasks or mark todos as "completed" unless:
- ✅ All tests pass (100% success rate)
- ✅ All linting passes (zero errors)
- ✅ All workflows are valid

## Hook Execution Points

| Hook Event | File | When | Can Block? |
|------------|------|------|------------|
| PostToolUse | `post-code-change.sh` | After Edit/Write on .go files | ⚠️ Shows errors |
| Stop | `enforce-quality-gate.sh` | Before Claude completes response | 🔒 **BLOCKS** |
| SubagentStop | `enforce-quality-gate.sh` | Before subagent completes | 🔒 **BLOCKS** |
| UserPromptSubmit | `pre-user-prompt-submit.sh` | Before sending to user | 🔒 **BLOCKS** |

## Validation Commands

```bash
# Full validation (what runs on Stop hook)
.claude/hooks/pre-completion-validator.sh

# Quick validation (what runs on code changes)
.claude/hooks/post-code-change.sh Edit path/to/file.go

# Test manually
make test-unit           # Run all unit tests
make lint                # Run linter
act --dryrun -W .github/workflows/test.yml  # Validate workflow
```

## Exit Codes

| Code | Meaning | Effect | Used By |
|------|---------|--------|---------|
| 0 | Success | Allow continuation | All hooks |
| 1 | Non-blocking error | Show warning, continue | PostToolUse |
| 2 | Blocking error | **STOP Claude**, must fix | Stop, SubagentStop |

## What Claude Sees

### When Blocked ❌
```
VALIDATION FAILED: Cannot complete task.
Linting, tests, or workflows have errors.
Fix all issues before proceeding.
```

### When Passing ✅
```
✅ All quality gates passed:
Linting ✓, Tests ✓, Workflows ✓
```

## Common Issues

| Problem | Solution |
|---------|----------|
| Hook not running | Check executable: `chmod +x .claude/hooks/*.sh` |
| Tests timing out | Increase timeout in `settings.local.json` |
| False positives | Run validation manually to debug |
| Workflow validation fails | Install act: `brew install act` |

## Quick Fixes

```bash
# Fix permissions
chmod +x .claude/hooks/*.sh

# Validate configuration
cat .claude/settings.local.json | jq .

# Test a specific hook
.claude/hooks/pre-completion-validator.sh

# See what changed
git diff --name-only HEAD

# Disable temporarily (NOT RECOMMENDED)
mv .claude/hooks/enforce-quality-gate.sh{,.disabled}
# Re-enable immediately after:
mv .claude/hooks/enforce-quality-gate.sh{.disabled,}
```

## Key Files

```
.claude/
├── settings.local.json           # Hook configuration
└── hooks/
    ├── README.md                 # Full documentation
    ├── QUALITY_GATE_GUIDE.md     # Detailed guide
    ├── QUICK_REFERENCE.md        # This file
    ├── post-code-change.sh       # Immediate feedback on edits
    ├── pre-completion-validator.sh  # Full validation suite
    ├── enforce-quality-gate.sh   # Smart gate enforcer
    └── pre-user-prompt-submit.sh # Final safety check
```

## Configuration Location

```json
// .claude/settings.local.json
{
  "hooks": {
    "PostToolUse": [...],    // After code changes
    "Stop": [...],           // Before completion
    "SubagentStop": [...],   // Before subtask completion
    "UserPromptSubmit": [...] // Before sending response
  }
}
```

## For Claude

When you see validation errors:
1. Read the error message carefully
2. Run the suggested fix commands
3. DO NOT mark todos as "completed" until all pass
4. DO NOT claim success until validation passes
5. Re-run validation after fixes

## For Developers

To modify validation requirements:
1. Edit `.claude/hooks/pre-completion-validator.sh`
2. Add/remove validation steps
3. Test changes: `.claude/hooks/pre-completion-validator.sh`
4. Update timeouts if needed in `settings.local.json`

## Emergency Bypass (Use Sparingly)

```bash
# Disable all quality gates (NOT RECOMMENDED)
# Only use for debugging, re-enable immediately after

# Backup current config
cp .claude/settings.local.json .claude/settings.local.json.backup

# Remove hooks section from settings.local.json
jq 'del(.hooks)' .claude/settings.local.json > /tmp/settings.json
mv /tmp/settings.json .claude/settings.local.json

# To restore:
mv .claude/settings.local.json.backup .claude/settings.local.json
```

⚠️ **WARNING:** Disabling quality gates removes all safety nets. Only do this for debugging.
