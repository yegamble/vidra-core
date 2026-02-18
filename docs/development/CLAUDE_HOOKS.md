# Claude Code Hooks Documentation

This document explains the Claude Code hooks system used in the Athena project for automated code quality assurance and workflow optimization.

## Overview

Claude Code hooks are automated scripts that run at specific points in the development workflow to ensure code quality, run tests, and maintain consistency. The hooks integrate with specialized agents for Go backend development and testing.

## Hook System Architecture

```
.claude/
├── hooks/
│   ├── post-code-change.sh          # Runs after code changes
│   └── pre-user-prompt-submit.sh    # Runs before submitting prompts
└── agents/
    ├── go-backend-reviewer/         # Code review agent
    └── golang-test-guardian/        # Test coverage agent
```

## Available Hooks

### 1. post-code-change.sh

**Trigger**: Automatically runs after any code changes are made.

**Purpose**:

- Run linters and formatters
- Execute affected tests
- Validate code quality
- Check for common issues

**Typical Actions**:

```bash
# Format code
gofmt -w .

# Run linters
golangci-lint run ./...

# Run affected tests
go test ./...

# Check for credential leaks
git secrets --scan
```

**Configuration**:
Set environment variables to customize behavior:

```bash
CLAUDE_SKIP_LINT=false       # Skip linting
CLAUDE_SKIP_TESTS=false      # Skip tests
CLAUDE_VERBOSE=true          # Verbose output
```

### 2. pre-user-prompt-submit.sh

**Trigger**: Runs before submitting a user prompt to Claude Code.

**Purpose**:

- Gather context about current state
- Run pre-checks
- Prepare environment
- Validate prerequisites

**Typical Actions**:

```bash
# Check git status
git status --short

# Verify migrations are up to date
make migrate-status

# Check for uncommitted changes
git diff --stat

# Validate environment
make check-env
```

## Agent Integration

### go-backend-reviewer Agent

**Purpose**: Automated code review for Go backend changes.

**Capabilities**:

- Architectural consistency checks
- Security vulnerability scanning
- Performance optimization suggestions
- Best practice validation
- Dependency analysis

**Invocation**:

```bash
# Automatic via hooks
post-code-change.sh → go-backend-reviewer

# Manual invocation
claude agent run go-backend-reviewer
```

**Review Criteria**:

1. **Architecture Compliance**
   - Layered architecture adherence (domain → usecase → repository)
   - Dependency injection patterns
   - Interface design

2. **Security**
   - Input validation
   - SQL injection prevention
   - Authentication/authorization
   - Credential handling

3. **Performance**
   - Database query optimization
   - Goroutine management
   - Memory allocations
   - Context handling

4. **Testing**
   - Test coverage requirements (>85%)
   - Test isolation
   - Mock usage
   - Integration test coverage

### golang-test-guardian Agent

**Purpose**: Comprehensive test coverage analysis and gap identification.

**Capabilities**:

- Test coverage calculation
- Missing test identification
- Test quality assessment
- Critical path analysis
- Edge case detection

**Invocation**:

```bash
# Automatic via hooks
post-code-change.sh → golang-test-guardian

# Manual invocation
claude agent run golang-test-guardian
```

**Analysis Features**:

- **Coverage by Package**: Package-level test coverage metrics
- **Critical Gaps**: Untested security-critical code paths
- **Edge Cases**: Missing boundary condition tests
- **Integration Tests**: Cross-package integration coverage
- **Benchmark Tests**: Performance regression detection

## Hook Workflow Examples

### Example 1: Making Code Changes

```bash
# 1. Developer makes code changes
vim internal/usecase/video_service.go

# 2. Save changes
# → post-code-change.sh automatically runs

# 3. Hook actions:
#    - gofmt formats the code
#    - golangci-lint checks for issues
#    - go test runs affected tests
#    - go-backend-reviewer analyzes changes
#    - golang-test-guardian checks coverage

# 4. Results displayed:
#    ✅ Format: OK
#    ✅ Lint: OK
#    ✅ Tests: 15/15 passed
#    ⚠️  Coverage: 82% (below 85% threshold)
#    📊 Review: 3 suggestions
```

### Example 2: Pre-Commit Workflow

```bash
# 1. Developer prepares commit
git add .

# 2. Pre-commit hook runs (if configured)
# → pre-user-prompt-submit.sh runs

# 3. Hook actions:
#    - Check for credential leaks
#    - Validate YAML files
#    - Check struct alignment
#    - Run security scanners

# 4. Prevent commit if issues found:
#    ❌ Credential leak detected: .env file
#    ❌ YAML syntax error: config.yaml
#    → Commit blocked
```

### Example 3: Submitting Prompt to Claude

```bash
# 1. User types prompt in Claude Code
# "Add rate limiting to video upload endpoint"

# 2. pre-user-prompt-submit.sh runs

# 3. Hook actions:
#    - Gather current rate limiting implementations
#    - Check for related middleware
#    - Identify test files to update
#    - Prepare context for Claude

# 4. Enriched prompt submitted with context:
#    - Current rate limiter: Redis sliding window
#    - Existing middleware: /internal/middleware/ratelimit.go
#    - Test file: /internal/middleware/ratelimit_test.go
#    - Coverage: 95%
```

## Installation & Setup

### 1. Install Pre-commit Hook

```bash
# Copy hook to git hooks
cp .claude/hooks/post-code-change.sh .git/hooks/post-commit
chmod +x .git/hooks/post-commit
```

### 2. Configure Hook Behavior

Create `.claude/config.yaml`:

```yaml
hooks:
  post-code-change:
    enabled: true
    run_lint: true
    run_tests: true
    run_agents: true
    agents:
      - go-backend-reviewer
      - golang-test-guardian

  pre-user-prompt-submit:
    enabled: true
    gather_context: true
    max_context_files: 10
```

### 3. Verify Installation

```bash
# Test hook execution
.claude/hooks/post-code-change.sh

# Expected output:
# ✓ Hooks installed
# ✓ Agents available
# ✓ Configuration valid
```

## Troubleshooting

### Hook Not Running

**Issue**: Hook doesn't execute after code changes.

**Solutions**:

1. Check hook is executable: `chmod +x .claude/hooks/*.sh`
2. Verify git hooks are installed: `ls -la .git/hooks/`
3. Check CLAUDE_SKIP_* environment variables

### Agent Failures

**Issue**: Agent runs but reports errors.

**Solutions**:

1. Update agents: `claude agent update`
2. Check agent logs: `~/.claude/logs/agents/`
3. Verify required tools installed:
   - `golangci-lint --version`
   - `go test -version`
   - `git secrets --version`

### Performance Issues

**Issue**: Hooks slow down development workflow.

**Solutions**:

1. Limit scope: Only run tests for changed packages
2. Parallelize: Run agents concurrently
3. Cache: Enable Go build cache
4. Skip: Use `CLAUDE_SKIP_TESTS=true` for rapid iteration

## Best Practices

### 1. Run Full Checks Before Commits

```bash
# Run comprehensive checks
make pre-commit

# Equivalent to:
gofmt -w .
golangci-lint run ./...
go test -race -coverprofile=coverage.out ./...
git secrets --scan
```

### 2. Use Hooks for Rapid Feedback

Let hooks catch issues early:

- Format issues → Fixed automatically
- Lint issues → Caught before commit
- Test failures → Prevented from CI
- Security issues → Blocked immediately

### 3. Customize for Your Workflow

Adjust hook behavior based on context:

```bash
# Quick iteration (skip tests)
CLAUDE_SKIP_TESTS=true vim internal/usecase/video_service.go

# Security-focused (extra checks)
CLAUDE_SECURITY_MODE=strict git commit
```

### 4. Review Agent Suggestions

Agents provide suggestions, not requirements:

- ✅ Review each suggestion
- ✅ Understand the reasoning
- ✅ Apply if beneficial
- ❌ Don't blindly accept all

## Integration with CI/CD

Hooks complement CI/CD pipelines:

**Local (Hooks)**:

- Fast feedback (seconds)
- Subset of checks
- Developer experience
- Rapid iteration

**CI/CD (GitHub Actions)**:

- Comprehensive checks (minutes)
- Full test suite
- Cross-platform validation
- Deployment gates

**Example CI/CD Integration**:

```yaml
# .github/workflows/test.yml
name: Test
on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4

      # Run same checks as hooks
      - name: Format
        run: gofmt -l .

      - name: Lint
        run: golangci-lint run ./...

      - name: Test
        run: go test -race -coverprofile=coverage.out ./...

      - name: Coverage
        run: go tool cover -func=coverage.out
```

## See Also

- [Testing Strategy](TESTING_STRATEGY.md) - Comprehensive testing approach
- [Code Quality Review](CODE_QUALITY_REVIEW.md) - Quality standards
- [Development Guide](../claude/contributing.md) - Contributing guidelines
- [Pre-commit Configuration](../../.pre-commit-config.yaml) - Pre-commit hooks

## References

- [Claude Code Documentation](https://docs.anthropic.com/claude-code)
- [Go Testing Best Practices](https://go.dev/doc/tutorial/add-a-test)
- [golangci-lint Configuration](https://golangci-lint.run/usage/configuration/)
- [Git Hooks Documentation](https://git-scm.com/docs/githooks)
