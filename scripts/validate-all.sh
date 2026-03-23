#!/usr/bin/env bash

#
# validate-all.sh - Portable validation script for Vidra Core project
#
# This script runs all code quality validations in a portable manner.
# It works across macOS, Linux, and Windows (WSL/Git Bash/MSYS2).
#
# Usage:
#   ./scripts/validate-all.sh
#   make validate-all
#
# Exit codes:
#   0 = All validations passed
#   1 = One or more validations failed
#

set -e  # Exit on first error
set -u  # Exit on undefined variable

# Colors for output (disabled if not a TTY)
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

# Script directory (works on macOS, Linux, Windows)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Counters
TOTAL_CHECKS=0
PASSED_CHECKS=0
FAILED_CHECKS=0
SKIPPED_CHECKS=0

# Track failures
FAILURES=()

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Vidra Core Validation Suite${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""
echo "Project root: $PROJECT_ROOT"
echo "Platform: $(uname -s)"
echo ""

# Change to project root
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
    echo -e "${BLUE}[$((TOTAL_CHECKS + 1))] $1${NC}"
    echo "----------------------------------------"
}

print_success() {
    echo -e "${GREEN}✓ PASSED${NC}: $1"
}

print_failure() {
    echo -e "${RED}✗ FAILED${NC}: $1"
}

print_skip() {
    echo -e "${YELLOW}⊘ SKIPPED${NC}: $1"
}

print_warning() {
    echo -e "${YELLOW}⚠ WARNING${NC}: $1"
}

record_pass() {
    PASSED_CHECKS=$((PASSED_CHECKS + 1))
    TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
}

record_fail() {
    local msg="$1"
    FAILED_CHECKS=$((FAILED_CHECKS + 1))
    TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
    FAILURES+=("$msg")
}

record_skip() {
    SKIPPED_CHECKS=$((SKIPPED_CHECKS + 1))
    TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
}

#
# Validation 1: Go Installation
#

print_header "Go Installation Check"

if check_command go; then
    GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
    echo "Go version: $GO_VERSION"

    # Check minimum version (1.23.4)
    REQUIRED_VERSION="1.23.4"
    if [ "$(printf '%s\n' "$REQUIRED_VERSION" "$GO_VERSION" | sort -V | head -n1)" = "$REQUIRED_VERSION" ]; then
        print_success "Go version $GO_VERSION >= $REQUIRED_VERSION"
        record_pass
    else
        print_failure "Go version $GO_VERSION < $REQUIRED_VERSION"
        record_fail "Go version too old"
    fi
else
    print_failure "Go is not installed"
    record_fail "Go not installed"
    echo ""
    echo "Install Go from: https://golang.org/dl/"
    exit 1
fi

#
# Validation 2: Dependencies Check
#

print_header "Go Dependencies Check"

if go mod verify >/dev/null 2>&1; then
    print_success "go.mod and go.sum are valid"
    record_pass
else
    print_failure "go mod verify failed"
    record_fail "go mod verify failed"
    echo ""
    echo "Run: go mod tidy"
fi

#
# Validation 3: Auto-Format Code
#

print_header "Auto-Formatting Code (gofmt)"

# Run formatter
if make fmt >/dev/null 2>&1; then
    # Check if any files were modified
    MODIFIED_FILES=$(git diff --name-only 2>/dev/null || true)

    if [ -n "$MODIFIED_FILES" ]; then
        print_success "Formatted and staged the following files:"
        for file in $MODIFIED_FILES; do
            echo "  → $file"
            git add "$file" 2>/dev/null || true
        done
        record_pass
    else
        print_success "All Go files already formatted correctly"
        record_pass
    fi
else
    print_warning "Could not run make fmt (non-critical)"
    record_skip
fi

#
# Validation 4: Import Sorting
#

print_header "Import Sorting Check (goimports)"

if check_command goimports; then
    UNSORTED=$(find . -name '*.go' -not -path './vendor/*' -exec goimports -l {} + 2>/dev/null || echo "")

    if [ -z "$UNSORTED" ]; then
        print_success "All imports are sorted correctly"
        record_pass
    else
        print_failure "The following files need import sorting:"
        echo "$UNSORTED"
        record_fail "goimports sorting required"
        echo ""
        echo "Run: make fmt"
    fi
else
    print_warning "goimports not installed"
    print_warning "Install with: go install golang.org/x/tools/cmd/goimports@latest"
    record_skip
fi

#
# Validation 5: Linting
#

print_header "Linting (golangci-lint)"

if check_command golangci-lint; then
    # Run golangci-lint
    if golangci-lint run ./... 2>&1; then
        print_success "No linting issues found"
        record_pass
    else
        print_failure "Linting issues found"
        record_fail "golangci-lint errors"
        echo ""
        echo "Fix the issues above or run: make lint (auto-fixes some issues)"
    fi
else
    print_warning "golangci-lint not installed"
    print_warning "Install with: brew install golangci-lint (macOS) or see https://golangci-lint.run/usage/install/"
    record_skip
fi

#
# Validation 6: YAML Validation (Optional)
#

print_header "YAML Validation (pre-commit)"

if check_command pre-commit; then
    if [ -f ".pre-commit-config.yaml" ]; then
        # Run only yamllint hook
        if pre-commit run yamllint --all-files 2>&1; then
            print_success "YAML files are valid"
            record_pass
        else
            print_failure "YAML validation failed"
            record_fail "yamllint errors"
            echo ""
            echo "Fix YAML formatting issues above"
        fi
    else
        print_skip ".pre-commit-config.yaml not found"
        record_skip
    fi
else
    print_skip "pre-commit not installed (optional)"
    record_skip
fi

#
# Validation 7: Unit Tests
#

print_header "Unit Tests"

# Check if running in CI (skip tests that require services)
if [ "${CI:-false}" = "true" ]; then
    print_warning "Running in CI mode - skipping tests that require services"
fi

# Exclude integration tests and repository tests (which need DB)
PKGS=$(go list ./... | grep -v "/internal/repository$" | grep -v "^vidra/tests/integration$" || echo "")

if [ -z "$PKGS" ]; then
    print_warning "No test packages found"
    record_skip
else
    echo "Running unit tests (excluding integration tests)..."

    # Run tests with short flag to skip long-running tests
    if go test -v -race -short -timeout=5m $PKGS 2>&1; then
        print_success "All unit tests passed"
        record_pass
    else
        print_failure "Some unit tests failed"
        record_fail "unit tests failed"
        echo ""
        echo "Review the test failures above"
    fi
fi

#
# Validation 8: Coverage Threshold Check
#

print_header "Coverage Threshold Check"

COVERAGE_THRESHOLD="${COVERAGE_THRESHOLD:-50}"

echo "Running coverage analysis (threshold: ${COVERAGE_THRESHOLD}%)..."

COVERAGE_OUT=$(mktemp)
if go test -coverprofile="$COVERAGE_OUT" ./... >/dev/null 2>&1; then
    COVERAGE=$(go tool cover -func="$COVERAGE_OUT" | grep '^total:' | awk '{print $NF}' | tr -d '%')
    rm -f "$COVERAGE_OUT"

    echo "Total coverage: ${COVERAGE}%"

    # Compare as integers (truncate decimal) to avoid bc dependency
    COVERAGE_INT=${COVERAGE%%.*}
    THRESHOLD_INT=${COVERAGE_THRESHOLD%%.*}

    if [ "$COVERAGE_INT" -ge "$THRESHOLD_INT" ]; then
        print_success "Coverage ${COVERAGE}% meets threshold ${COVERAGE_THRESHOLD}%"
        record_pass
    else
        print_failure "Coverage ${COVERAGE}% is below threshold ${COVERAGE_THRESHOLD}%"
        record_fail "coverage below ${COVERAGE_THRESHOLD}%"
    fi
else
    rm -f "$COVERAGE_OUT"
    print_warning "Could not compute coverage (tests may have failed above)"
    record_skip
fi

#
# Validation 9: Build Verification
#

print_header "Build Verification"

echo "Building server binary..."

if go build -o /tmp/vidra-server-validate ./cmd/server 2>&1; then
    print_success "Build successful"
    record_pass

    # Clean up
    rm -f /tmp/vidra-server-validate
else
    print_failure "Build failed"
    record_fail "build errors"
    echo ""
    echo "Fix the compilation errors above"
fi

#
# Validation 10: Vet Check
#

print_header "Go Vet Check"

if go vet ./... 2>&1; then
    print_success "go vet found no issues"
    record_pass
else
    print_failure "go vet found issues"
    record_fail "go vet errors"
    echo ""
    echo "Fix the issues reported by go vet"
fi

#
# Summary
#

echo ""
echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Validation Summary${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""
echo "Total checks: $TOTAL_CHECKS"
echo -e "${GREEN}Passed: $PASSED_CHECKS${NC}"
echo -e "${RED}Failed: $FAILED_CHECKS${NC}"
echo -e "${YELLOW}Skipped: $SKIPPED_CHECKS${NC}"
echo ""

if [ $FAILED_CHECKS -eq 0 ]; then
    echo -e "${GREEN}========================================${NC}"
    echo -e "${GREEN}ALL VALIDATIONS PASSED ✓${NC}"
    echo -e "${GREEN}========================================${NC}"
    exit 0
else
    echo -e "${RED}========================================${NC}"
    echo -e "${RED}VALIDATIONS FAILED ✗${NC}"
    echo -e "${RED}========================================${NC}"
    echo ""
    echo "Failed checks:"
    for failure in "${FAILURES[@]}"; do
        echo -e "${RED}  ✗ $failure${NC}"
    done
    echo ""
    echo "Please fix the issues above before proceeding."
    exit 1
fi
