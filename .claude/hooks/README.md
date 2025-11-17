# Claude Code Hooks

This directory contains hooks that ensure code quality and business logic integrity when Claude makes changes to the codebase.

## Available Hooks

### 1. `post-code-change.sh`

**Trigger:** Runs after `Edit` or `Write` tool calls on Go files

**Purpose:** Validates that code changes maintain quality and don't break tests

**What it does:**
- Runs `golangci-lint` on the modified file
- Executes tests for the affected package
- Provides recommendations to use specialized agents for critical files

**When it blocks:**
- Linter finds issues in the modified file
- Tests fail in the affected package

**How to fix:**
- Follow the hook's recommendations to use `go-backend-reviewer` or `golang-test-guardian` agents
- Address linter issues and test failures before proceeding

### 2. `pre-user-prompt-submit.sh`

**Trigger:** Runs before Claude sends a response to the user (when Go files were modified)

**Purpose:** Final validation that the codebase is in a healthy state

**What it does:**
- Checks all modified Go files with linter (fast mode)
- Runs quick tests on affected packages
- Warns if critical business logic files were modified

**When it blocks:**
- Any modified file has linter issues
- Any affected package has failing tests

**How to fix:**
- Review the hook output for specific failures
- Use the recommended agents to resolve issues
- Ensure all tests pass before submission

## Hook Integration with Agents

The hooks work in conjunction with specialized agents:

### Code Quality Issues → `go-backend-reviewer`
When hooks detect linter issues or code quality problems, they recommend using the `go-backend-reviewer` agent for:
- Go best practices compliance
- Architecture pattern validation
- Performance and concurrency review

### Business Logic Issues → `golang-test-guardian`
When hooks detect test failures or critical file modifications, they recommend using the `golang-test-guardian` agent for:
- Business logic preservation validation
- API contract verification
- Test coverage quality assessment

### Security Issues → `decentralized-systems-security-expert`
For security-critical changes (auth, crypto, IPFS, payments), use this agent for:
- Security vulnerability analysis
- Cryptographic practice review
- Decentralized system security audit

## Critical Files Monitoring

Hooks pay special attention to files in these directories:
- `/internal/usecase/` - Business logic layer
- `/internal/repository/` - Data access layer
- `/internal/domain/` - Domain models and core business rules
- `/internal/httpapi/handlers/` - API endpoints
- `/internal/security/` - Security-critical code

Changes to these files trigger additional warnings and recommendations.

## Workflow Example

1. Claude modifies `internal/usecase/video_service.go`
2. `post-code-change.sh` runs automatically:
   - Lints the file ✅
   - Runs tests in `internal/usecase/` ✅
   - Warns: "Critical business logic file modified"
   - Recommends: "Run golang-test-guardian agent"
3. Claude uses `golang-test-guardian` to validate business logic preservation
4. Before sending response, `pre-user-prompt-submit.sh` runs:
   - Quick lint check on all modified files ✅
   - Quick test run on affected packages ✅
   - Final validation passes ✅
5. Response sent to user with confidence that code quality is maintained

## Disabling Hooks (Not Recommended)

If you need to temporarily disable hooks for debugging:

```bash
# Rename hooks to prevent execution
mv .claude/hooks/post-code-change.sh .claude/hooks/post-code-change.sh.disabled
mv .claude/hooks/pre-user-prompt-submit.sh .claude/hooks/pre-user-prompt-submit.sh.disabled
```

**Warning:** Disabling hooks removes quality guardrails. Only do this for debugging and re-enable immediately after.

## Testing Hooks Locally

You can test hooks manually:

```bash
# Test post-code-change hook
.claude/hooks/post-code-change.sh Edit internal/usecase/video_service.go

# Test pre-submit hook
.claude/hooks/pre-user-prompt-submit.sh
```

## Benefits

✅ **Prevents regressions** - Tests run automatically after code changes
✅ **Maintains code quality** - Linter enforces standards consistently
✅ **Protects business logic** - Critical files get extra scrutiny
✅ **Fast feedback** - Issues caught immediately, not in CI
✅ **Agent recommendations** - Hooks guide Claude to use the right tools
✅ **Confidence** - User receives responses knowing code was validated

## Customization

To adjust hook behavior, edit the shell scripts:
- **Timeout adjustments**: Change `-timeout` values in test commands
- **Linter strictness**: Modify `golangci-lint` flags (e.g., add `--fast` for speed)
- **Coverage requirements**: Add coverage checks with `go test -cover`
- **Additional validations**: Add custom checks for your project needs

## Troubleshooting

**Hook fails but I can't see why:**
- Run the hook manually (see "Testing Hooks Locally" above)
- Check hook script permissions: `ls -la .claude/hooks/`
- Review hook output for specific error messages

**Tests take too long:**
- Add `-short` flag to test commands for faster smoke tests
- Adjust timeout values for your environment
- Consider running only affected tests instead of full package

**False positives:**
- Review linter configuration in `.golangci.yml`
- Ensure test fixtures and dependencies are available
- Check that git status reflects actual changes

## References

- [Claude Code Hooks Documentation](https://code.claude.com/docs/en/hooks)
- [golangci-lint Configuration](../../.golangci.yml)
- [Agent Definitions](../agents/)
