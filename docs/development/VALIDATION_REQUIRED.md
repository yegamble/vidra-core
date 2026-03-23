# VALIDATION REQUIRED - READ THIS FIRST

## CRITICAL: ALL CODE CHANGES REQUIRE VALIDATION

This file exists to ensure that **EVERY Claude instance** (web interface, CLI, or any other client) on **ANY machine** (Mac, Linux, Windows) **MUST run validations** before claiming success on any code changes.

---

## FOR ALL CLAUDE INSTANCES

### YOU MUST RUN VALIDATIONS BEFORE CLAIMING SUCCESS

**This applies to:**

- Claude Code CLI
- Claude web interface
- Claude API
- Any other Claude client or interface
- Any operating system (macOS, Linux, Windows)
- Any machine or environment

### DO NOT SKIP VALIDATIONS

If you are Claude and you have made ANY code changes, you **MUST**:

1. **Run the validation script**: `./scripts/validate-all.sh`
2. **Review all validation results**
3. **Fix any failures before claiming success**
4. **Include validation results in your response to the user**

### If Validations Are Not Available

If you cannot run validations (e.g., in Claude web interface without shell access):

1. **State clearly**: "I cannot run validations in this environment"
2. **Provide instructions**: Tell the user exactly what commands to run
3. **Do NOT claim the work is complete**
4. **Remind the user**: Validation is required before using the changes

---

## VALIDATION COMMANDS

### Quick Validation (Recommended)

```bash
# Run all validations at once
make validate-all
```

### Individual Validations

```bash
# 1. Code formatting check
make fmt-check

# 2. Linting
make lint

# 3. YAML validation (if pre-commit is installed)
pre-commit run yamllint --all-files || echo "Pre-commit not available"

# 4. Unit tests
make test-unit

# 5. Build verification
make build
```

### Manual Validation Script

```bash
# If make is not available, run the script directly
./scripts/validate-all.sh
```

---

## WHAT GETS VALIDATED

### 1. Code Formatting

- Go formatting (gofmt)
- Import sorting (goimports)
- No formatting violations allowed

### 2. Linting

- golangci-lint checks
- Security vulnerabilities (gosec)
- Code quality issues
- Dead code detection

### 3. YAML Files

- YAML syntax validation
- Indentation and formatting
- Schema validation where applicable

### 4. Tests

- Unit tests must pass
- No race conditions
- Test coverage requirements

### 5. Build

- Code must compile successfully
- No build errors
- Dependencies must resolve

---

## VALIDATION FAILURES

### If Validations Fail

**DO NOT claim success.** Instead:

1. **Review the errors** from the validation output
2. **Fix the issues** in the code
3. **Re-run validations** until they pass
4. **Only then** report completion to the user

### Common Failure Scenarios

- **Formatting issues**: Run `make fmt` to auto-fix
- **Linting errors**: Address each issue individually
- **Test failures**: Debug and fix failing tests
- **Build errors**: Resolve compilation or dependency issues

---

## FOR CLAUDE WEB INTERFACE

If you are using Claude through the web interface (chat.anthropic.com):

### You Cannot Run Shell Commands

The web interface does not have access to shell/terminal, so you cannot run validation scripts directly.

### What You MUST Do

1. **Make your code changes** as requested
2. **Tell the user**: "I've made the changes, but validations are required"
3. **Provide exact commands**:

   ```
   Please run these commands to validate:

   cd /path/to/vidra
   make validate-all

   Or individually:
   make fmt-check
   make lint
   make test-unit
   make build
   ```

4. **Explain**: What each validation checks and why it's important
5. **Do NOT say**: "The changes are complete and tested" (they are not tested until validations pass)

---

## FOR CLAUDE CODE CLI

If you are using Claude Code (CLI interface with shell access):

### You CAN Run Validations

You have access to the Bash tool and can run validation commands.

### What You MUST Do

1. **Make your code changes** as requested
2. **Run validations immediately**: `make validate-all` or `./scripts/validate-all.sh`
3. **Review the output** for any failures
4. **Fix any failures** and re-run validations
5. **Include validation results** in your response to the user
6. **Only claim success** after all validations pass

### Example Workflow

```bash
# After making code changes...

# Run all validations
make validate-all

# If failures occur, fix them, then re-run
make validate-all

# Report to user with results
```

---

## PORTABLE ACROSS MACHINES

This validation system is designed to work on:

- **macOS** (Intel and Apple Silicon)
- **Linux** (Ubuntu, Debian, Fedora, Alpine, etc.)
- **Windows** (via WSL, Git Bash, or MSYS2)

### Dependencies

The validation scripts check for required tools and provide installation instructions if missing:

- Go (1.23.4+)
- golangci-lint
- goimports
- pre-commit (optional, for YAML validation)

### No Hardcoded Paths

All scripts use relative paths or detect tools dynamically, ensuring portability.

---

## GIT PRE-COMMIT HOOKS (BACKUP)

As a backup, Git pre-commit hooks are available to run validations automatically on commit.

### Setup (Optional)

```bash
# Run the setup script
./scripts/setup-validation.sh

# Or manually
chmod +x .githooks/pre-commit
git config core.hooksPath .githooks
```

### What the Hook Does

Before each `git commit`:

1. Runs `make fmt-check` (formatting)
2. Runs `make lint` (linting)
3. Runs `pre-commit run --all-files` (YAML, etc.)
4. Blocks commit if any validation fails

---

## NO EXCUSES

### This is NOT Optional

Validations are **mandatory** for all code changes. There are no exceptions.

### Why This Matters

- **Code Quality**: Ensures consistent, high-quality code
- **Security**: Catches vulnerabilities before they reach production
- **Reliability**: Prevents broken builds and test failures
- **Maintainability**: Keeps the codebase clean and readable

### If You Skip Validations

- Code may not compile
- Tests may fail in CI
- Security vulnerabilities may be introduced
- Code quality degrades
- Other developers waste time fixing issues

---

## SUMMARY FOR CLAUDE

### Before Responding to User

- [ ] Code changes made
- [ ] Validations run (or user instructed to run them)
- [ ] All validations passed (or failures addressed)
- [ ] Results included in response
- [ ] User knows whether validations passed

### Template Response (If Validations Not Run)

```
I've made the requested changes to [files].

IMPORTANT: Validations are required before using these changes.

Please run:
  make validate-all

Or individually:
  make fmt-check  # Check formatting
  make lint       # Check code quality
  make test-unit  # Run tests
  make build      # Verify build

Let me know if any validations fail and I'll help fix them.
```

### Template Response (If Validations Passed)

```
I've made the requested changes to [files].

Validations passed:
  ✓ Formatting check (make fmt-check)
  ✓ Linting (make lint)
  ✓ Unit tests (make test-unit)
  ✓ Build (make build)

The changes are ready to use.
```

---

## ENFORCEMENT

This validation requirement is enforced through:

1. **This documentation** (you are reading it now)
2. **CLAUDE.md** (strict requirements for AI assistants)
3. **Validation scripts** (automated checks)
4. **Git hooks** (pre-commit validation)
5. **CI/CD pipelines** (automated validation on push)
6. **Code review** (human verification)

**If you are Claude, you are expected to follow these requirements without exception.**

---

## QUESTIONS?

If you're unsure about any validation requirement, ask the user or refer to:

- `/Users/yosefgamble/github/vidra/docs/architecture/CLAUDE.md` - AI assistant requirements
- `/Users/yosefgamble/github/vidra/Makefile` - Available validation targets
- `/Users/yosefgamble/github/vidra/scripts/validate-all.sh` - Validation script
- This file - Validation requirements

---

**Last Updated**: 2025-11-16
**Applies To**: All Claude instances, all environments, all machines
