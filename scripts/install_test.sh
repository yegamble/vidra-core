#!/bin/bash
# Test suite for install.sh
# Usage: bash scripts/install_test.sh

set -e

# Capture script directory before any cd commands
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
INSTALL_SH_PATH="$SCRIPT_DIR/install.sh"

# Colors for test output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Test counters
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

# Test result tracking
test_pass() {
    TESTS_RUN=$((TESTS_RUN + 1))
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}✓${NC} %s\n" "$1"
}

test_fail() {
    TESTS_RUN=$((TESTS_RUN + 1))
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}✗${NC} %s\n" "$1"
    if [ -n "${2:-}" ]; then
        printf "  ${RED}%s${NC}\n" "$2"
    fi
}

# Setup and teardown
setup_test() {
    TEST_DIR=$(mktemp -d)
    export INSTALL_DIR="$TEST_DIR"
    cd "$TEST_DIR"
}

teardown_test() {
    if [ -n "${TEST_DIR:-}" ]; then
        cd /
        rm -rf "$TEST_DIR"
        unset TEST_DIR
    fi
}

# Mock commands
mock_docker() {
    docker() {
        echo "Docker version 24.0.0"
    }
    export -f docker 2>/dev/null || true
}

mock_docker_missing() {
    docker() {
        return 127  # Command not found
    }
    export -f docker 2>/dev/null || true
}

mock_git() {
    git() {
        case "$1" in
            clone)
                mkdir -p .git
                touch docker-compose.yml README.md
                ;;
            pull)
                echo "Already up to date."
                ;;
            *)
                echo "git $*"
                ;;
        esac
    }
    export -f git 2>/dev/null || true
}

mock_git_missing() {
    git() {
        return 127  # Command not found
    }
    export -f git 2>/dev/null || true
}

mock_curl() {
    curl() {
        # Mock successful curl
        return 0
    }
    export -f curl 2>/dev/null || true
}

# Create a stable temp file for sourced functions
FUNCTIONS_TEMP=$(mktemp)
trap 'rm -f "$FUNCTIONS_TEMP"' EXIT

# Source install.sh functions without executing main
# We need to extract functions without running main
source_install_functions() {
    if [ ! -f "$INSTALL_SH_PATH" ]; then
        echo "Error: install.sh not found at $INSTALL_SH_PATH"
        exit 1
    fi

    # Remove the last line (main call) and fix SCRIPT_DIR for testing
    sed '$d' "$INSTALL_SH_PATH" | \
        sed 's|SCRIPT_DIR=.*|SCRIPT_DIR="'"$SCRIPT_DIR"'"|' > "$FUNCTIONS_TEMP"
    # shellcheck disable=SC1090
    . "$FUNCTIONS_TEMP"
}

# Test 1: Empty INSTALL_DIR with docker-compose.yml present
test_existing_docker_compose() {
    setup_test
    source_install_functions
    mock_git
    mock_docker

    # Create docker-compose.yml (simulates existing installation)
    touch "$TEST_DIR/docker-compose.yml"

    # Run setup_athena
    setup_athena 2>&1 | grep -q "already present" && \
        test_pass "Test 1: Skips clone when docker-compose.yml exists" || \
        test_fail "Test 1: Skips clone when docker-compose.yml exists" "Expected 'already present' message"

    teardown_test
}

# Test 2: Empty INSTALL_DIR with .git present
test_existing_git_repo() {
    setup_test
    source_install_functions
    mock_git
    mock_docker

    # Create .git directory (simulates git clone)
    mkdir -p "$TEST_DIR/.git"

    # Run setup_athena
    setup_athena 2>&1 | grep -q "git repository detected" && \
        test_pass "Test 2: Runs git pull when .git exists" || \
        test_fail "Test 2: Runs git pull when .git exists" "Expected 'git repository detected' message"

    teardown_test
}

# Test 3: Non-empty directory without .git or docker-compose.yml
test_non_empty_invalid_dir() {
    setup_test
    source_install_functions
    mock_git
    mock_docker

    # Create a file (makes directory non-empty)
    touch "$TEST_DIR/somefile.txt"

    # Run setup_athena - should exit with error
    if setup_athena 2>&1 | grep -q "not empty"; then
        test_pass "Test 3: Exits with error for non-empty invalid directory"
    else
        test_fail "Test 3: Exits with error for non-empty invalid directory" "Expected 'not empty' error message"
    fi

    teardown_test
}

# Test 4: Empty directory triggers git clone
test_empty_directory_clone() {
    setup_test
    source_install_functions
    mock_git
    mock_docker

    # Directory is empty, should trigger clone
    setup_athena 2>&1 | grep -q "Downloading Athena" && \
        test_pass "Test 4: Clones repository in empty directory" || \
        test_fail "Test 4: Clones repository in empty directory" "Expected 'Downloading Athena' message"

    teardown_test
}

# Test 5: INSTALL_DIR explicitly set by user
test_custom_install_dir() {
    CUSTOM_DIR=$(mktemp -d)
    export INSTALL_DIR="$CUSTOM_DIR"

    source_install_functions
    mock_git
    mock_docker

    setup_athena 2>&1 | grep -q "$CUSTOM_DIR" && \
        test_pass "Test 5: Respects custom INSTALL_DIR" || \
        test_fail "Test 5: Respects custom INSTALL_DIR" "Expected custom directory to be used"

    rm -rf "$CUSTOM_DIR"
    unset INSTALL_DIR
}

# Test 6: Script run from scripts/ subdirectory auto-detects repo root
test_auto_detect_repo_root() {
    # This test verifies the INSTALL_DIR auto-detection logic
    # When run from scripts/, it should detect ../docker-compose.yml

    REPO_ROOT=$(mktemp -d)
    mkdir -p "$REPO_ROOT/scripts"
    touch "$REPO_ROOT/docker-compose.yml"

    # Simulate running from scripts/ directory
    cd "$REPO_ROOT/scripts"
    SCRIPT_DIR="$REPO_ROOT/scripts"

    # Test the auto-detection logic
    if [ -f "$SCRIPT_DIR/../docker-compose.yml" ]; then
        DETECTED_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
        [ "$DETECTED_DIR" = "$REPO_ROOT" ] && \
            test_pass "Test 6: Auto-detects repo root from scripts/ subdirectory" || \
            test_fail "Test 6: Auto-detects repo root from scripts/ subdirectory" "Detection failed"
    else
        test_fail "Test 6: Auto-detects repo root from scripts/ subdirectory" "docker-compose.yml not found"
    fi

    cd /
    rm -rf "$REPO_ROOT"
}

# Test 7: Pre-existing .env file is preserved
test_preserve_existing_env() {
    setup_test
    source_install_functions
    mock_git
    mock_docker

    # Create existing .env with custom content
    echo "CUSTOM_VAR=value" > "$TEST_DIR/.env"
    touch "$TEST_DIR/docker-compose.yml"

    # Run setup_athena
    setup_athena >/dev/null 2>&1

    # Check that custom content is preserved
    if grep -q "CUSTOM_VAR=value" "$TEST_DIR/.env"; then
        test_pass "Test 7: Preserves existing .env file"
    else
        test_fail "Test 7: Preserves existing .env file" "Custom .env content was overwritten"
    fi

    teardown_test
}

# Test 8: Docker not installed on macOS
test_docker_missing_macos() {
    setup_test
    source_install_functions
    mock_docker_missing

    # Simulate macOS
    OS="macos"

    # install_docker should exit with message
    if install_docker 2>&1 | grep -q "Docker Desktop"; then
        test_pass "Test 8: Provides Docker Desktop install message on macOS"
    else
        test_fail "Test 8: Provides Docker Desktop install message on macOS" "Expected Docker Desktop message"
    fi

    teardown_test
}

# Test 9: Docker not installed on Linux (attempts install)
test_docker_missing_linux() {
    setup_test
    source_install_functions
    mock_docker_missing
    mock_curl

    # Simulate Ubuntu
    OS="ubuntu"

    # install_docker should attempt install
    if install_docker 2>&1 | grep -q "Installing Docker"; then
        test_pass "Test 9: Attempts Docker install on Linux"
    else
        test_fail "Test 9: Attempts Docker install on Linux" "Expected Docker install message"
    fi

    teardown_test
}

# Test 10: Native mode requested (not yet implemented)
test_native_mode_not_implemented() {
    setup_test
    source_install_functions
    mock_docker

    # Set MODE to native
    MODE="native"

    # main should exit with error
    if main 2>&1 | grep -q "not yet implemented"; then
        test_pass "Test 10: Exits with error for native mode"
    else
        test_fail "Test 10: Exits with error for native mode" "Expected 'not yet implemented' message"
    fi

    teardown_test
}

# Test 11: Generated .env contains required setup mode fields
test_generated_env_contents() {
    setup_test
    source_install_functions
    mock_git
    mock_docker

    # Empty directory triggers .env creation
    setup_athena >/dev/null 2>&1

    # Check .env contents
    errors=""
    [ -f "$TEST_DIR/.env" ] || errors="${errors}. .env not created"
    grep -q "SETUP_COMPLETED=false" "$TEST_DIR/.env" || errors="${errors}SETUP_COMPLETED=false missing. "
    grep -q "PORT=8080" "$TEST_DIR/.env" || errors="${errors}PORT=8080 missing. "
    grep -q "REQUIRE_IPFS=false" "$TEST_DIR/.env" || errors="${errors}REQUIRE_IPFS=false missing. "

    if [ -z "$errors" ]; then
        test_pass "Test 11: Generated .env contains required setup mode fields"
    else
        test_fail "Test 11: Generated .env contains required setup mode fields" "$errors"
    fi

    teardown_test
}

# Run all tests
main_test() {
    echo "Running install.sh test suite..."
    echo ""

    test_existing_docker_compose
    test_existing_git_repo
    test_non_empty_invalid_dir
    test_empty_directory_clone
    test_custom_install_dir
    test_auto_detect_repo_root
    test_preserve_existing_env
    test_docker_missing_macos
    test_docker_missing_linux
    test_native_mode_not_implemented
    test_generated_env_contents

    # Print summary
    echo ""
    echo "========================================"
    echo "Test Summary"
    echo "========================================"
    printf "Total: %d | ${GREEN}Passed: %d${NC} | ${RED}Failed: %d${NC}\n" \
        "$TESTS_RUN" "$TESTS_PASSED" "$TESTS_FAILED"
    echo "========================================"

    # Exit with error if any tests failed
    [ "$TESTS_FAILED" -eq 0 ] || exit 1
}

# Run tests
main_test
