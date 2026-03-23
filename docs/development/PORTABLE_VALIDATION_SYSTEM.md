# Portable Validation System

## Overview

The Athena project includes a comprehensive, portable validation system that works across all platforms and environments, including:

- **Claude Code CLI** (with shell access)
- **Claude Web Interface** (without shell access)
- **macOS** (Intel and Apple Silicon)
- **Linux** (all distributions)
- **Windows** (WSL, Git Bash, MSYS2)
- **CI/CD pipelines** (GitHub Actions, GitLab CI, etc.)

## Key Features

### 1. Cross-Platform Compatibility

All validation scripts are written in portable Bash and work on any platform with Bash 4.0+.

**No hardcoded paths** - Scripts use relative paths and dynamic detection
**No OS-specific commands** - Only portable POSIX commands
**Graceful degradation** - Optional tools are skipped if not available

### 2. Multiple Validation Levels

#### Quick Validation

```bash
make validate-quick
```

- Code formatting check
- Linting
- ~30 seconds

#### Full Validation

```bash
make validate-all
```

- Code formatting check
- Import sorting check
- Comprehensive linting
- YAML validation
- Unit tests
- Build verification
- Go vet checks
- ~2-5 minutes

#### Individual Checks

```bash
make fmt-check   # Formatting only
make lint        # Linting only
make test-unit   # Tests only
make build       # Build only
```

### 3. Works Without Hooks

Unlike pre-commit hooks that only work in git environments, this system:

- Works in any directory
- Works on fresh clones
- Works without git
- Works in CI/CD
- Works for all users (no setup required)

### 4. Claude-Aware

The system includes special documentation and requirements for Claude AI instances:

**For Claude Code (CLI)**:

- Must run validations before claiming success
- Must include validation results in response
- Must fix failures before completing

**For Claude Web**:

- Must instruct user to run validations
- Must provide exact commands
- Must not claim work is complete without user confirmation

See `/Users/yosefgamble/github/athena/VALIDATION_REQUIRED.md` for full requirements.

## Architecture

### File Structure

```
athena/
├── VALIDATION_REQUIRED.md          # Main requirements document (Claude-focused)
├── .validation-quickref.md         # Quick reference card
├── README.md                       # Updated with validation info
├── Makefile                        # New validate-all and validate-quick targets
├── docs/
│   ├── architecture/
│   │   └── CLAUDE.md              # Updated with validation requirements
│   └── development/
│       └── PORTABLE_VALIDATION_SYSTEM.md  # This file
├── scripts/
│   ├── validate-all.sh            # Main validation script (portable)
│   ├── setup-validation.sh        # Setup script (installs tools, configures hooks)
│   └── README_VALIDATION.md       # Complete validation documentation
└── .githooks/
    └── pre-commit                 # Git pre-commit hook (backup validation)
```

### Components

#### 1. `VALIDATION_REQUIRED.md`

**Purpose**: Primary documentation for all users and Claude instances
**Audience**: Developers, AI assistants, contributors
**Content**:

- Mandatory validation requirements
- Platform-specific instructions
- Commands to run
- What gets validated
- How to handle failures
- Specific instructions for Claude instances

#### 2. `scripts/validate-all.sh`

**Purpose**: Main validation script
**Features**:

- Portable Bash (works on Mac, Linux, Windows)
- No hardcoded paths
- Colored output (with TTY detection)
- Detailed progress reporting
- Comprehensive error messages
- Exit codes (0 = pass, 1 = fail)

**Checks**:

1. Go installation (version check)
2. Dependency validation (go mod verify)
3. Code formatting (gofmt)
4. Import sorting (goimports)
5. Linting (golangci-lint)
6. YAML validation (pre-commit/yamllint)
7. Unit tests (excluding integration tests)
8. Build verification
9. Go vet (static analysis)

#### 3. `scripts/setup-validation.sh`

**Purpose**: Interactive setup for new machines
**Features**:

- Checks for required tools
- Optionally installs missing tools
- Configures Git hooks
- Runs initial validation

**What it sets up**:

- golangci-lint installation
- goimports installation
- pre-commit installation (optional)
- Git hooks configuration
- Validation script permissions

#### 4. `.githooks/pre-commit`

**Purpose**: Git pre-commit hook (backup/additional validation)
**When it runs**: Before each `git commit`
**What it does**:

- Runs `make fmt-check`
- Runs `make lint`
- Runs `pre-commit run --all-files` (if available)
- Blocks commit if any check fails

**Setup**:

```bash
git config core.hooksPath .githooks
```

**Bypass** (emergency only):

```bash
git commit --no-verify
```

#### 5. `Makefile` Targets

**New targets**:

```makefile
validate-all:     # Run all validation checks
validate-quick:   # Quick validation (format + lint)
```

**Existing targets** (still available):

```makefile
fmt-check:   # Check formatting
fmt:         # Auto-fix formatting
lint:        # Run linter (auto-fixes some issues)
test-unit:   # Run unit tests
build:       # Build server
```

### Validation Workflow

```
┌─────────────────────────────────────┐
│  Developer makes code changes       │
└─────────────┬───────────────────────┘
              │
              ▼
┌─────────────────────────────────────┐
│  Run: make validate-all             │
│  OR: ./scripts/validate-all.sh      │
└─────────────┬───────────────────────┘
              │
              ▼
┌─────────────────────────────────────┐
│  Validation checks run sequentially │
│  1. Go installation                 │
│  2. Dependencies                    │
│  3. Code formatting                 │
│  4. Import sorting                  │
│  5. Linting                         │
│  6. YAML validation                 │
│  7. Unit tests                      │
│  8. Build verification              │
│  9. Go vet                          │
└─────────────┬───────────────────────┘
              │
              ▼
        ┌─────┴─────┐
        │           │
        ▼           ▼
    ┌───────┐   ┌───────┐
    │ PASS  │   │ FAIL  │
    └───┬───┘   └───┬───┘
        │           │
        │           ▼
        │   ┌───────────────────┐
        │   │  Review failures  │
        │   │  Fix issues       │
        │   │  Re-run           │
        │   └───────┬───────────┘
        │           │
        │           ▼
        │   ┌───────────────────┐
        │   │  make fmt         │
        │   │  make lint        │
        │   │  Fix tests/code   │
        │   └───────┬───────────┘
        │           │
        └───────────┴───────────┐
                                │
                                ▼
                        ┌───────────────┐
                        │  Commit code  │
                        └───────┬───────┘
                                │
                                ▼
                        ┌───────────────┐
                        │  Git hook     │
                        │  validates    │
                        │  (optional)   │
                        └───────┬───────┘
                                │
                                ▼
                        ┌───────────────┐
                        │  Push to CI   │
                        └───────┬───────┘
                                │
                                ▼
                        ┌───────────────┐
                        │  CI validates │
                        │  (same checks)│
                        └───────────────┘
```

## Usage Scenarios

### Scenario 1: Developer on macOS

```bash
# First time
git clone <repo>
cd athena
./scripts/setup-validation.sh  # Interactive setup

# Daily workflow
# ... make code changes ...
make validate-all              # Before commit
git commit -m "changes"        # Pre-commit hook runs automatically
```

### Scenario 2: Developer on Linux

```bash
# First time
git clone <repo>
cd athena
./scripts/setup-validation.sh  # Interactive setup

# Daily workflow
# ... make code changes ...
make validate-all
git commit -m "changes"
```

### Scenario 3: Developer on Windows (WSL)

```bash
# First time
git clone <repo>
cd athena
./scripts/setup-validation.sh  # Interactive setup

# Daily workflow
# ... make code changes ...
make validate-all
git commit -m "changes"
```

### Scenario 4: Claude Code (CLI)

```bash
# Claude makes code changes
# ... edits files ...

# Claude MUST run validations
make validate-all

# Claude includes results in response:
# "Validations passed:
#   ✓ Formatting check
#   ✓ Linting
#   ✓ Tests
#   ✓ Build"
```

### Scenario 5: Claude Web (No Shell Access)

```
Claude: "I've made the changes to [files].

IMPORTANT: Validations are required before using these changes.

Please run:
  cd /path/to/athena
  make validate-all

This will check:
  - Code formatting
  - Linting
  - Tests
  - Build

Let me know if any validations fail and I'll help fix them."
```

### Scenario 6: CI/CD Pipeline

```yaml
# GitHub Actions
- name: Run validations
  run: make validate-all
```

```yaml
# GitLab CI
validate:
  script:
    - make validate-all
```

## Configuration

### Required Tools

- **Go 1.23.4+**: Language runtime
- **Git**: Version control
- **Bash 4.0+**: Script execution

### Optional Tools

- **golangci-lint**: Linting (highly recommended)
- **goimports**: Import sorting (highly recommended)
- **pre-commit**: YAML validation (optional)
- **make**: Build automation (recommended)

### Installation

#### macOS (Homebrew)

```bash
brew install go git golangci-lint
go install golang.org/x/tools/cmd/goimports@latest
brew install pre-commit  # optional
```

#### Linux (Ubuntu/Debian)

```bash
# Install Go from golang.org
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin
go install golang.org/x/tools/cmd/goimports@latest
pip3 install --user pre-commit  # optional
```

#### Windows (WSL)

```bash
# Install Go from golang.org
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin
go install golang.org/x/tools/cmd/goimports@latest
pip3 install --user pre-commit  # optional
```

## Troubleshooting

### Common Issues

#### "Go version too old"

**Solution**: Update Go to 1.23.4 or later

```bash
# macOS
brew upgrade go

# Linux/Windows WSL
# Download from golang.org
```

#### "golangci-lint not installed"

**Solution**: Install golangci-lint

```bash
# macOS
brew install golangci-lint

# Linux/Windows WSL
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin
```

#### "Permission denied" on scripts

**Solution**: Make scripts executable

```bash
chmod +x scripts/*.sh
chmod +x .githooks/*
```

#### "Command not found: make"

**Solution**: Run script directly

```bash
./scripts/validate-all.sh
```

Or install make:

```bash
# macOS
xcode-select --install

# Ubuntu/Debian
sudo apt-get install build-essential

# Windows WSL
sudo apt-get install build-essential
```

### Debugging

#### Verbose Output

```bash
# Run script with set -x for debugging
bash -x ./scripts/validate-all.sh
```

#### Individual Checks

```bash
# Run checks individually to isolate issues
make fmt-check
make lint
make test-unit
make build
```

#### Skip Optional Checks

Pre-commit is optional. If it's causing issues:

1. Check `.pre-commit-config.yaml` is valid
2. Run `pre-commit run --all-files` separately
3. Or skip it (validation will continue)

## Performance

### Validation Times

| Check | Duration | Can Skip? |
|-------|----------|-----------|
| Go installation | <1s | No |
| Dependencies | <5s | No |
| Code formatting | 1-5s | No |
| Import sorting | 1-5s | No |
| Linting | 10-30s | No |
| YAML validation | 5-15s | Yes (optional) |
| Unit tests | 30s-3m | No |
| Build | 5-30s | No |
| Go vet | 5-15s | No |

**Total**: 1-5 minutes for full validation

### Optimization Tips

1. **Quick validation** for rapid iteration:

   ```bash
   make validate-quick  # Just format + lint (~30s)
   ```

2. **Parallel tests** (if supported):

   ```bash
   go test -parallel 8 ...
   ```

3. **Incremental linting**:

   ```bash
   golangci-lint run --new-from-rev=HEAD~1
   ```

## CI/CD Integration

### GitHub Actions

```yaml
name: Validate

on: [push, pull_request]

jobs:
  validate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - uses: actions/setup-go@v4
        with:
          go-version: '1.23.4'

      - name: Install tools
        run: |
          go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
          go install golang.org/x/tools/cmd/goimports@latest

      - name: Run validations
        run: make validate-all
```

### GitLab CI

```yaml
validate:
  image: golang:1.23.4
  before_script:
    - go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
    - go install golang.org/x/tools/cmd/goimports@latest
  script:
    - make validate-all
```

### Docker

```dockerfile
# Multi-stage build with validation
FROM golang:1.23.4 as validator

WORKDIR /app
COPY . .

RUN go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest && \
    go install golang.org/x/tools/cmd/goimports@latest

RUN make validate-all

# Continue with build...
```

## Best Practices

### 1. Run Before Commit

Always run validations before committing:

```bash
make validate-all
git commit
```

### 2. Use Git Hooks

Enable pre-commit hooks for automatic validation:

```bash
./scripts/setup-validation.sh
```

### 3. Fix Issues Promptly

Don't accumulate validation failures. Fix them as you go:

```bash
# Auto-fix what's possible
make fmt
make lint

# Then re-validate
make validate-all
```

### 4. CI/CD as Gate

Use validation in CI/CD as a quality gate. Don't merge if validation fails.

### 5. Document Custom Checks

If you add custom validation checks, document them in:

- `scripts/validate-all.sh`
- `scripts/README_VALIDATION.md`
- This file

## Future Enhancements

Potential improvements to the validation system:

1. **Performance optimizations**:
   - Parallel check execution
   - Incremental validation (only changed files)
   - Caching of validation results

2. **Additional checks**:
   - Security scanning (gosec in depth)
   - Dependency auditing (go mod audit)
   - License compliance
   - Documentation coverage

3. **Better reporting**:
   - HTML reports
   - JSON output for CI/CD
   - Metrics tracking over time

4. **Integration**:
   - IDE plugins (VSCode, GoLand)
   - Git GUI integration
   - Pre-push validation

## Support

For issues or questions:

1. Check `scripts/README_VALIDATION.md`
2. Check `VALIDATION_REQUIRED.md`
3. Check this file
4. Open an issue in the repository

## License

Same as the Athena project.
