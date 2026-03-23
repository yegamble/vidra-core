#!/usr/bin/env bash

#
# setup-validation.sh - Setup validation environment for Vidra Core project
#
# This script sets up all validation tools and Git hooks.
# It's designed to be portable across macOS, Linux, and Windows (WSL/Git Bash).
#
# Usage:
#   ./scripts/setup-validation.sh
#
# What it does:
#   1. Checks for required tools (Go, git, make)
#   2. Installs optional tools (golangci-lint, goimports, pre-commit)
#   3. Sets up Git hooks for pre-commit validation
#   4. Validates the setup
#

set -e  # Exit on error
set -u  # Exit on undefined variable

# Colors for output
if [ -t 1 ]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[1;33m'
    BLUE='\033[0;34m'
    NC='\033[0m' # No Color
else
    RED=''
    GREEN=''
    YELLOW=''
    BLUE=''
    NC=''
fi

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Vidra Core Validation Setup${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""
echo "Project root: $PROJECT_ROOT"
echo "Platform: $(uname -s)"
echo ""

cd "$PROJECT_ROOT"

#
# Helper functions
#

check_command() {
    local cmd="$1"
    if command -v "$cmd" >/dev/null 2>&1; then
        return 0
    else
        return 1
    fi
}

print_header() {
    echo ""
    echo -e "${BLUE}$1${NC}"
    echo "----------------------------------------"
}

print_success() {
    echo -e "${GREEN}✓${NC} $1"
}

print_info() {
    echo -e "${BLUE}ℹ${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

print_error() {
    echo -e "${RED}✗${NC} $1"
}

#
# Step 1: Check required tools
#

print_header "Checking Required Tools"

# Go
if check_command go; then
    GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
    print_success "Go $GO_VERSION installed"
else
    print_error "Go is not installed"
    echo "Install from: https://golang.org/dl/"
    exit 1
fi

# Git
if check_command git; then
    GIT_VERSION=$(git --version | awk '{print $3}')
    print_success "Git $GIT_VERSION installed"
else
    print_error "Git is not installed"
    exit 1
fi

# Make
if check_command make; then
    print_success "Make installed"
else
    print_warning "Make is not installed (optional, but recommended)"
fi

#
# Step 2: Install/check optional tools
#

print_header "Checking Optional Tools"

# golangci-lint
if check_command golangci-lint; then
    LINT_VERSION=$(golangci-lint --version | head -n1 | awk '{print $4}')
    print_success "golangci-lint $LINT_VERSION installed"
else
    print_warning "golangci-lint not installed"
    echo ""
    read -p "Install golangci-lint? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        print_info "Installing golangci-lint..."

        # Detect platform
        if [[ "$OSTYPE" == "darwin"* ]]; then
            # macOS
            if check_command brew; then
                brew install golangci-lint
                print_success "golangci-lint installed via Homebrew"
            else
                curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b "$(go env GOPATH)/bin"
                print_success "golangci-lint installed to $(go env GOPATH)/bin"
            fi
        else
            # Linux/WSL
            curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b "$(go env GOPATH)/bin"
            print_success "golangci-lint installed to $(go env GOPATH)/bin"
        fi
    fi
fi

# goimports
if check_command goimports; then
    print_success "goimports installed"
else
    print_warning "goimports not installed"
    echo ""
    read -p "Install goimports? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        print_info "Installing goimports..."
        go install golang.org/x/tools/cmd/goimports@latest
        print_success "goimports installed to $(go env GOPATH)/bin"
    fi
fi

# pre-commit (Python tool)
if check_command pre-commit; then
    print_success "pre-commit installed"
else
    print_warning "pre-commit not installed (optional, for YAML validation)"
    echo ""
    read -p "Install pre-commit? Requires Python/pip (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        print_info "Installing pre-commit..."

        if check_command pip3; then
            pip3 install --user pre-commit
            print_success "pre-commit installed via pip3"
        elif check_command pip; then
            pip install --user pre-commit
            print_success "pre-commit installed via pip"
        elif check_command brew; then
            brew install pre-commit
            print_success "pre-commit installed via Homebrew"
        else
            print_error "Could not install pre-commit (no pip or brew found)"
            print_info "Install manually: https://pre-commit.com/#install"
        fi
    fi
fi

#
# Step 3: Setup Git hooks
#

print_header "Setting Up Git Hooks"

# Create .githooks directory if it doesn't exist
mkdir -p "$PROJECT_ROOT/.githooks"

# Create pre-commit hook
cat > "$PROJECT_ROOT/.githooks/pre-commit" <<'EOF'
#!/usr/bin/env bash

#
# Git pre-commit hook for Vidra Core project
#
# - Enforces gofmt on staged Go files
# - Re-stages any files that were reformatted
# - Runs lint and optional pre-commit hooks
#
# To bypass (not recommended): git commit --no-verify

set -euo pipefail

echo "Running pre-commit validations..."
echo ""

# Change to repository root
cd "$(git rev-parse --show-toplevel)"

# Timeout controls (seconds). Set to 0 to disable timeouts.
LINT_TIMEOUT_SECONDS="${LINT_TIMEOUT_SECONDS:-180}"
HOOK_TIMEOUT_SECONDS="${HOOK_TIMEOUT_SECONDS:-240}"
# Default skip list for slow hooks; set PRECOMMIT_SKIP_HOOKS="" to run everything.
PRECOMMIT_SKIP_HOOKS="${PRECOMMIT_SKIP_HOOKS-go-unit-tests,test-coverage}"

# Run a command with a timeout. Returns 124 on timeout.
run_with_timeout() {
    local timeout_secs="$1"
    shift

    if [ "$timeout_secs" -le 0 ]; then
        "$@"
        return $?
    fi

    if command -v python3 >/dev/null 2>&1; then
        python3 - "$timeout_secs" "$@" <<'PY'
import os
import signal
import subprocess
import sys

timeout = int(sys.argv[1])
cmd = sys.argv[2:]
proc = subprocess.Popen(cmd, preexec_fn=os.setsid)

try:
    proc.wait(timeout=timeout)
    sys.exit(proc.returncode)
except subprocess.TimeoutExpired:
    try:
        os.killpg(proc.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass

    try:
        proc.wait(timeout=2)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        proc.wait()

    sys.exit(124)
PY
        return $?
    fi

    "$@"
}

# Track failures
FAILURES=0

# 1) Format only staged Go files and re-stage them
echo "[1/3] Enforcing gofmt on staged Go files..."

if ! command -v gofmt >/dev/null 2>&1; then
    echo "✗ gofmt not found in PATH. Please install Go toolchain."
    exit 1
fi

STAGED_GO_FILES=()
while IFS= read -r -d '' staged_file; do
    case "$staged_file" in
        *.go) STAGED_GO_FILES+=("$staged_file") ;;
    esac
done < <(git diff --cached --name-only --diff-filter=ACMR -z)

if [ "${#STAGED_GO_FILES[@]}" -eq 0 ]; then
    echo "✓ No staged Go files"
else
    NEED_FMT_FILES=()
    while IFS= read -r file_to_format; do
        [ -n "$file_to_format" ] && NEED_FMT_FILES+=("$file_to_format")
    done < <(gofmt -s -l "${STAGED_GO_FILES[@]}" || true)

    if [ "${#NEED_FMT_FILES[@]}" -gt 0 ]; then
        echo "Formatting and re-staging:"
        gofmt -s -w "${NEED_FMT_FILES[@]}"
        for f in "${NEED_FMT_FILES[@]}"; do
            echo "  → $f"
            git add -- "$f"
        done
        echo "✓ gofmt applied to ${#NEED_FMT_FILES[@]} file(s)"
    else
        echo "✓ All staged Go files are gofmt clean"
    fi
fi

# 2) Lint (may auto-fix import groups if configured)
echo "[2/3] Running linter..."
if run_with_timeout "$LINT_TIMEOUT_SECONDS" make lint; then
    echo "✓ Linting passed"
else
    lint_rc=$?
    if [ "$lint_rc" -eq 124 ]; then
        echo "✗ Linting timed out after ${LINT_TIMEOUT_SECONDS}s"
    else
        echo "✗ Linting failed"
    fi
    echo "  Fix the issues or run: make lint"
    FAILURES=$((FAILURES + 1))
fi

# 3) Run optional pre-commit hooks (e.g., yamllint) if available
if command -v pre-commit >/dev/null 2>&1; then
    if [ "$FAILURES" -gt 0 ]; then
        echo "[3/3] Skipping pre-commit hooks (previous checks failed)"
    else
        echo "[3/3] Running pre-commit hooks on staged files..."
        if [ -n "$PRECOMMIT_SKIP_HOOKS" ]; then
            echo "  Skipping slow hooks: $PRECOMMIT_SKIP_HOOKS"
        fi

        if [ -n "$PRECOMMIT_SKIP_HOOKS" ]; then
            if run_with_timeout "$HOOK_TIMEOUT_SECONDS" env SKIP="$PRECOMMIT_SKIP_HOOKS" pre-commit run --hook-stage commit; then
                echo "✓ Pre-commit hooks passed"
            else
                precommit_rc=$?
                if [ "$precommit_rc" -eq 124 ]; then
                    echo "✗ Pre-commit hooks timed out after ${HOOK_TIMEOUT_SECONDS}s"
                else
                    echo "✗ Pre-commit hooks failed"
                fi
                FAILURES=$((FAILURES + 1))
            fi
        else
            if run_with_timeout "$HOOK_TIMEOUT_SECONDS" pre-commit run --hook-stage commit; then
                echo "✓ Pre-commit hooks passed"
            else
                precommit_rc=$?
                if [ "$precommit_rc" -eq 124 ]; then
                    echo "✗ Pre-commit hooks timed out after ${HOOK_TIMEOUT_SECONDS}s"
                else
                    echo "✗ Pre-commit hooks failed"
                fi
                FAILURES=$((FAILURES + 1))
            fi
        fi
    fi
else
    echo "[3/3] Skipping pre-commit hooks (not installed)"
fi

echo ""

if [ $FAILURES -eq 0 ]; then
    echo "✓ All pre-commit validations passed"
    exit 0
else
    echo "✗ $FAILURES validation(s) failed"
    echo ""
    echo "Fix the issues above or use --no-verify to bypass (not recommended)"
    exit 1
fi
EOF

# Make hook executable
chmod +x "$PROJECT_ROOT/.githooks/pre-commit"
print_success "Pre-commit hook created at .githooks/pre-commit"

# Configure Git to use .githooks directory
if git config core.hooksPath .githooks 2>/dev/null; then
    print_success "Git configured to use .githooks directory"
else
    print_warning "Could not configure Git hooks path"
    echo "  Run manually: git config core.hooksPath .githooks"
fi

#
# Step 4: Validate setup
#

print_header "Validating Setup"

if [ -x "$PROJECT_ROOT/scripts/validate-all.sh" ]; then
    print_info "Running validation script to verify setup..."
    echo ""

    if "$PROJECT_ROOT/scripts/validate-all.sh"; then
        print_success "Validation script completed successfully"
    else
        print_warning "Validation script found some issues (see above)"
        echo "  This is normal for a fresh setup"
    fi
else
    print_warning "Validation script not found or not executable"
fi

#
# Summary
#

echo ""
echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Setup Complete${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""
echo "Next steps:"
echo ""
echo "1. Run validations:"
echo "   make validate-all"
echo "   OR"
echo "   ./scripts/validate-all.sh"
echo ""
echo "2. Set up pre-commit hooks (optional):"
echo "   pre-commit install"
echo ""
echo "3. Read validation requirements:"
echo "   cat VALIDATION_REQUIRED.md"
echo ""
print_success "Setup complete!"
