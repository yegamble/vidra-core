# Claude Code Hooks

This directory contains hooks that ensure code quality and business logic integrity when Claude makes changes to the codebase.

## Quick Links

- **[Getting Started Guide](docs/GETTING_STARTED.md)** - Setup and quick start (5 minutes)
- **[Quick Reference](docs/QUICK_REFERENCE.md)** - TL;DR, commands, and exit codes
- **[Quality Gate Guide](docs/QUALITY_GATE_GUIDE.md)** - Detailed enforcement system explanation
- **[Architecture](docs/ARCHITECTURE.md)** - System architecture and flow diagrams
- **[Implementation Summary](docs/IMPLEMENTATION_SUMMARY.md)** - Technical implementation details

## Strict Quality Gate Enforcement

This project enforces a **zero-tolerance quality gate** that prevents Claude from completing tasks or marking todos as complete until ALL validation requirements pass:

- **100% test success rate** - All unit tests must pass
- **Zero linting errors** - All code must meet quality standards
- **Valid workflows** - All GitHub Actions workflows must validate
- **No regressions** - Code changes cannot break existing functionality

Claude **CANNOT**:
- Mark tasks as complete if tests fail
- Claim success if linting has errors
- Complete responses if workflows are invalid
- Mark todos as "completed" status until all gates pass

## Hook Enforcement Points

The quality gates are enforced at multiple points:

1. **PostToolUse** (after code changes) - Immediate feedback on code edits
2. **Stop** (before completing responses) - Blocks Claude from finishing until validation passes
3. **SubagentStop** (before completing subtasks) - Prevents task agents from claiming completion
4. **UserPromptSubmit** (before sending responses) - Final safety check before user sees results

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

### 3. `pre-completion-validator.sh`

**Trigger:** Runs on Stop/SubagentStop events (before Claude completes tasks)

**Purpose:** Enforces strict quality gate before allowing task completion

**What it does:**
- Runs full linting: `golangci-lint run --timeout 5m ./...`
- Runs all unit tests: `make test-unit`
- Validates GitHub workflows: `act --dryrun` on all workflow files
- Reports comprehensive validation status

**When it blocks:**
- ANY linting errors exist
- ANY tests fail (not 100% success)
- ANY workflows are invalid
- ANY quality gate fails

**Exit behavior:**
- Exit 0: All gates passed, allows completion
- Exit 2: Validation failed, **BLOCKS** completion with detailed error message

**How to fix:**
1. Fix all linting issues: `make lint`
2. Fix all test failures: `make test-unit`
3. Fix workflow errors: Validate `.github/workflows/*.yml`
4. Re-run validation until all pass

### 4. `enforce-quality-gate.sh`

**Trigger:** Runs on Stop/SubagentStop events

**Purpose:** Smart quality gate that only validates if code was changed

**What it does:**
- Checks git for modified Go files or workflows
- If changes detected, delegates to `pre-completion-validator.sh`
- If no changes, allows completion immediately

**When it blocks:**
- Code or workflows were modified AND validation fails

**How to fix:**
- Same as `pre-completion-validator.sh` above

### 5. `post-todo-update.sh`

**Trigger:** Runs after TodoWrite tool usage

**Purpose:** Prevents marking todos as "completed" when quality gates fail

**What it does:**
- Detects when todos are marked with `status: "completed"`
- If completion detected, runs full validation
- Blocks the todo update if validation fails

**When it blocks:**
- Todo marked as "completed" but tests/linting/workflows fail

**How to fix:**
- Fix all quality gate issues before marking todos complete
- Only mark todos complete after achieving actual success

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

## Workflow Examples

### Example 1: Code Change with Validation

1. Claude modifies `internal/usecase/video_service.go`
2. `post-code-change.sh` runs automatically (PostToolUse hook):
   - Lints the file ✅
   - Runs tests in `internal/usecase/` ✅
   - Warns: "Critical business logic file modified"
   - Recommends: "Run golang-test-guardian agent"
3. Claude uses `golang-test-guardian` to validate business logic preservation
4. Before completing response, `enforce-quality-gate.sh` runs (Stop hook):
   - Detects Go files were modified
   - Delegates to `pre-completion-validator.sh`
   - Runs full linting, all tests, workflow validation ✅
   - All gates pass ✅
5. `pre-user-prompt-submit.sh` runs (UserPromptSubmit hook):
   - Quick lint check on all modified files ✅
   - Quick test run on affected packages ✅
   - Final validation passes ✅
6. Response sent to user with confidence that code quality is maintained

### Example 2: Blocked Completion Due to Test Failures

1. Claude modifies code but introduces a bug
2. `post-code-change.sh` catches the issue:
   - Linting passes ✅
   - Tests FAIL ❌
   - Hook output shows failure to Claude
3. Claude attempts to fix the issue
4. Claude tries to complete the task
5. `enforce-quality-gate.sh` runs (Stop hook):
   - Detects modified files
   - Runs `pre-completion-validator.sh`
   - Tests still failing ❌
   - **Hook returns exit code 2 - BLOCKS completion**
   - Error message sent to Claude: "VALIDATION FAILED: Cannot complete task..."
6. Claude sees the blocking message and must fix the errors
7. Claude fixes the bug and tests pass
8. `enforce-quality-gate.sh` runs again:
   - All validation passes ✅
   - Completion allowed ✅
9. Response sent to user

### Example 3: TodoWrite Blocked

1. Claude makes code changes and runs tests
2. Some tests fail but Claude doesn't notice
3. Claude tries to mark todo as completed:
   ```
   TodoWrite: [{ content: "Fix auth bug", status: "completed" }]
   ```
4. `post-todo-update.sh` hook triggers:
   - Detects `status: "completed"` in the update
   - Runs `pre-completion-validator.sh`
   - Tests are failing ❌
   - **Hook returns exit code 2 - BLOCKS the TodoWrite**
   - Error message: "BLOCKED: You marked todos as 'completed' but tests are failing"
5. Claude receives the error and cannot mark the todo complete
6. Claude must fix the failing tests
7. After fixing, Claude can successfully mark todo as completed

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
✅ **Enforces honesty** - Claude cannot claim success without achieving it
✅ **100% test success** - Tasks cannot complete with failing tests
✅ **Workflow validation** - GitHub Actions workflows must be valid
✅ **Blocks false completions** - Todos cannot be marked complete with failures

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

## Documentation

For detailed information, see the documentation in the `docs/` subdirectory:

### Getting Started
- **[GETTING_STARTED.md](docs/GETTING_STARTED.md)**
  - 5-minute quick start guide
  - Installation verification
  - Testing the system
  - First-time setup

### Reference Guides
- **[QUICK_REFERENCE.md](docs/QUICK_REFERENCE.md)**
  - TL;DR summary
  - Hook execution points
  - Validation commands
  - Exit codes reference
  - What Claude sees when blocked

- **[QUALITY_GATE_GUIDE.md](docs/QUALITY_GATE_GUIDE.md)**
  - How quality gates work
  - Multi-layer enforcement
  - Hook execution flow
  - Validation requirements
  - Troubleshooting guide

### Technical Documentation
- **[ARCHITECTURE.md](docs/ARCHITECTURE.md)**
  - System architecture diagrams
  - Data flow visualization
  - Component interactions
  - Hook integration points

- **[IMPLEMENTATION_SUMMARY.md](docs/IMPLEMENTATION_SUMMARY.md)**
  - Implementation details
  - Hook script specifications
  - Technical decisions
  - Integration patterns

## External References

- [Claude Code Hooks Documentation](https://code.claude.com/docs/en/hooks)
- [golangci-lint Configuration](../../.golangci.yml)
- [Agent Definitions](../agents/)
