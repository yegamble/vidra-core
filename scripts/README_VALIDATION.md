# Validation System

This directory contains scripts for portable, cross-platform validation of the Vidra Core codebase.

## Quick Start

### Run All Validations

```bash
# Using Make (recommended)
make validate-all

# Or directly
./scripts/validate-all.sh
```

### Setup Validation Environment

```bash
# Interactive setup (installs tools, configures git hooks)
./scripts/setup-validation.sh
```

## Files

### `validate-all.sh`

Comprehensive validation script that checks:

1. **Go Installation**: Verifies Go 1.23.4+ is installed
2. **Dependencies**: Validates go.mod and go.sum
3. **Code Formatting**: Checks gofmt compliance
4. **Import Sorting**: Verifies goimports organization
5. **Linting**: Runs golangci-lint with all checks
6. **YAML Validation**: Validates YAML files (if pre-commit available)
7. **Unit Tests**: Runs unit tests (excluding integration tests)
8. **Build Verification**: Ensures code compiles
9. **Go Vet**: Runs static analysis

**Exit Codes:**

- `0`: All validations passed
- `1`: One or more validations failed

**Usage:**

```bash
./scripts/validate-all.sh
```

### `setup-validation.sh`

Interactive setup script that:

1. Checks for required tools (Go, git, make)
2. Optionally installs development tools:
   - golangci-lint
   - goimports
   - pre-commit
3. Creates and configures Git hooks
4. Runs initial validation

**Usage:**

```bash
./scripts/setup-validation.sh
```

## Platform Support

All scripts are designed to work on:

- **macOS** (Intel and Apple Silicon)
- **Linux** (Ubuntu, Debian, Fedora, Alpine, etc.)
- **Windows** (via WSL, Git Bash, or MSYS2)

### Platform-Specific Notes

#### macOS

- Can use Homebrew for tool installation (`brew install golangci-lint`)
- Full support for all features

#### Linux

- Tools installed via curl or go install
- Requires bash 4.0+
- Full support for all features

#### Windows

- Requires WSL2, Git Bash, or MSYS2
- Use forward slashes in paths
- Some color output may not work in older terminals

## Dependencies

### Required

- **Go 1.23.4+**: Core language runtime
- **Git**: Version control
- **Bash 4.0+**: Script execution

### Optional (Recommended)

- **golangci-lint**: Code linting and analysis
- **goimports**: Import sorting and formatting
- **pre-commit**: YAML and config file validation
- **make**: Build automation

### Installing Optional Tools

#### golangci-lint

```bash
# macOS (Homebrew)
brew install golangci-lint

# Linux/macOS (curl)
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin

# Windows (WSL)
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin
```

#### goimports

```bash
go install golang.org/x/tools/cmd/goimports@latest
```

#### pre-commit

```bash
# macOS (Homebrew)
brew install pre-commit

# Linux/macOS (pip)
pip3 install --user pre-commit

# Windows (WSL)
pip3 install --user pre-commit
```

## Git Hooks

The validation system includes a pre-commit hook that runs automatically before each commit.

### Setup

```bash
# Run setup script
./scripts/setup-validation.sh

# Or manually
chmod +x .githooks/pre-commit
git config core.hooksPath .githooks
```

### What the Hook Does

Before each `git commit`, it runs:

1. `make fmt-check` - Formatting validation
2. `make lint` - Code linting
3. `pre-commit run --all-files` - YAML validation (if available)

If any check fails, the commit is blocked.

### Bypassing the Hook

```bash
# Not recommended, but possible in emergencies
git commit --no-verify
```

## Validation Checks

### 1. Go Installation

Verifies Go is installed and meets minimum version requirements.

**Required Version**: 1.23.4+

**Failure Resolution**:

- Install Go from <https://golang.org/dl/>
- Update existing Go installation

### 2. Dependencies

Validates `go.mod` and `go.sum` integrity.

**Checks**:

- `go mod verify` passes
- No missing or extra dependencies

**Failure Resolution**:

```bash
go mod tidy
go mod verify
```

### 3. Code Formatting

Ensures all Go code is formatted with `gofmt`.

**Standard**: Official Go formatting

**Failure Resolution**:

```bash
make fmt
# Or
gofmt -w $(git ls-files "*.go")
```

### 4. Import Sorting

Verifies imports are sorted and grouped correctly.

**Tool**: goimports

**Failure Resolution**:

```bash
make fmt
# Or
goimports -w $(git ls-files "*.go")
```

### 5. Linting

Runs comprehensive code quality checks.

**Tool**: golangci-lint

**Checks Include**:

- Code style (gofmt, govet, staticcheck)
- Security issues (gosec)
- Code complexity (cyclop)
- Dead code (deadcode)
- Error handling (errcheck)
- And many more...

**Failure Resolution**:

```bash
# Auto-fix what's possible
make lint

# Manual fixes
golangci-lint run ./...
```

**Configuration**: `.golangci.yml`

### 6. YAML Validation

Validates YAML files for syntax and formatting.

**Tool**: pre-commit (yamllint hook)

**Optional**: Skipped if pre-commit not installed

**Failure Resolution**:

- Fix YAML syntax errors
- Ensure proper indentation
- Check for duplicate keys

### 7. Unit Tests

Runs unit tests (excluding integration tests).

**Command**: `go test -v -race -short`

**Excludes**:

- `/internal/repository` (requires database)
- `/tests/integration` (integration tests)

**Failure Resolution**:

- Debug failing tests
- Fix code issues
- Update test expectations

### 8. Build Verification

Ensures code compiles successfully.

**Command**: `go build -o /tmp/vidra-server-validate ./cmd/server`

**Failure Resolution**:

- Fix compilation errors
- Resolve missing dependencies
- Check import paths

### 9. Go Vet

Runs static analysis to find suspicious code.

**Command**: `go vet ./...`

**Checks**:

- Printf format strings
- Unreachable code
- Struct tags
- And more...

**Failure Resolution**:

- Address reported issues
- Review suspicious code patterns

## CI/CD Integration

The validation scripts integrate seamlessly with CI/CD pipelines.

### GitHub Actions Example

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

### Environment Variables

#### `CI`

Set to `true` in CI environments to skip tests requiring services.

```bash
CI=true ./scripts/validate-all.sh
```

## Troubleshooting

### "Go version too old"

**Problem**: Installed Go version is below 1.23.4

**Solution**:

1. Download latest Go from <https://golang.org/dl/>
2. Install and verify: `go version`
3. Re-run validation

### "golangci-lint not installed"

**Problem**: golangci-lint not in PATH

**Solution**:

```bash
# macOS
brew install golangci-lint

# Linux/Windows WSL
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin

# Add to PATH if needed
export PATH="$PATH:$(go env GOPATH)/bin"
```

### "goimports not installed"

**Problem**: goimports not in PATH

**Solution**:

```bash
go install golang.org/x/tools/cmd/goimports@latest

# Add to PATH if needed
export PATH="$PATH:$(go env GOPATH)/bin"
```

### "Permission denied"

**Problem**: Script not executable

**Solution**:

```bash
chmod +x scripts/validate-all.sh
chmod +x scripts/setup-validation.sh
chmod +x .githooks/pre-commit
```

### "pre-commit: command not found"

**Problem**: pre-commit not installed (optional dependency)

**Solution**:

```bash
# macOS
brew install pre-commit

# Linux/Windows WSL
pip3 install --user pre-commit
```

Or skip this check (it's optional).

### Tests fail in validation

**Problem**: Unit tests fail during validation

**Solution**:

1. Run tests individually: `make test-unit`
2. Debug specific failing tests
3. Ensure no database/service dependencies in unit tests
4. Fix code or test issues

## Performance

Typical validation times:

- **Quick validation** (fmt-check + lint): 5-30 seconds
- **Full validation** (all checks): 1-5 minutes
- **Depends on**: Code size, test count, machine specs

## For Claude Instances

See `/Users/yosefgamble/github/vidra/VALIDATION_REQUIRED.md` for requirements specific to Claude AI assistants.

**Key Points**:

- Claude Code (CLI): MUST run validations
- Claude Web: CANNOT run validations (instruct user)
- All instances: Must ensure validations pass before claiming success

## Support

For issues or questions:

1. Check this README
2. Review `/Users/yosefgamble/github/vidra/VALIDATION_REQUIRED.md`
3. Check `/Users/yosefgamble/github/vidra/docs/architecture/CLAUDE.md`
4. Open an issue in the repository

## License

Same as the Vidra Core project.
