#!/bin/bash
#
# Security Tests Runner
# Runs virus scanner and file type blocker tests with ClamAV
#

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

# Default values
RUN_MODE="docker"
VERBOSE=false
COVERAGE=false
BENCHMARK=false

# Usage
usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Run security tests (virus scanner and file type blocker)

OPTIONS:
    -m, --mode MODE      Test mode: docker (default), local, short
    -v, --verbose        Verbose test output
    -c, --coverage       Generate coverage report
    -b, --benchmark      Run benchmarks
    -h, --help           Show this help message

MODES:
    docker      Run with Docker Compose (includes ClamAV)
    local       Run against local ClamAV daemon
    short       Skip integration tests (no ClamAV needed)

EXAMPLES:
    $0                           # Run with Docker
    $0 -m local -v               # Run locally with verbose output
    $0 -c                        # Generate coverage report
    $0 -b                        # Run benchmarks

EOF
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -m|--mode)
            RUN_MODE="$2"
            shift 2
            ;;
        -v|--verbose)
            VERBOSE=true
            shift
            ;;
        -c|--coverage)
            COVERAGE=true
            shift
            ;;
        -b|--benchmark)
            BENCHMARK=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo -e "${RED}Unknown option: $1${NC}"
            usage
            exit 1
            ;;
    esac
done

# Build test flags
TEST_FLAGS="-timeout 10m"
if [ "$VERBOSE" = true ]; then
    TEST_FLAGS="$TEST_FLAGS -v"
fi
if [ "$COVERAGE" = true ]; then
    TEST_FLAGS="$TEST_FLAGS -coverprofile=coverage.out"
fi

# Header
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}  Security Module Test Suite${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""

# Run based on mode
case $RUN_MODE in
    docker)
        echo -e "${YELLOW}Mode: Docker Compose${NC}"
        echo "Starting ClamAV container..."

        cd "$PROJECT_ROOT"

        # Start ClamAV
        docker compose -f docker-compose.test.yml up -d clamav-test

        echo "Waiting for ClamAV to be ready (this may take 2-3 minutes)..."

        # Wait for healthy status
        MAX_WAIT=180  # 3 minutes
        WAITED=0
        while [ $WAITED -lt $MAX_WAIT ]; do
            if docker compose -f docker-compose.test.yml ps clamav-test | grep -q "healthy"; then
                echo -e "${GREEN}ClamAV is ready!${NC}"
                break
            fi
            sleep 5
            WAITED=$((WAITED + 5))
            echo "  Waiting... ($WAITED/${MAX_WAIT}s)"
        done

        if [ $WAITED -ge $MAX_WAIT ]; then
            echo -e "${RED}ClamAV failed to start in time${NC}"
            docker compose -f docker-compose.test.yml logs clamav-test
            exit 1
        fi

        # Set environment
        export CLAMAV_ADDRESS=localhost:3310
        export CLAMAV_TIMEOUT=60
        export TEST_QUARANTINE_DIR=/tmp/test-quarantine

        # Run tests against the package (collectively)
        echo ""
        echo -e "${YELLOW}Running tests...${NC}"
        go test $TEST_FLAGS ./internal/security

        # Cleanup
        echo ""
        echo "Cleaning up..."
        docker compose -f docker-compose.test.yml down
        ;;

    local)
        echo -e "${YELLOW}Mode: Local ClamAV${NC}"

        # Check if ClamAV is running
        if ! nc -z localhost 3310 2>/dev/null; then
            echo -e "${RED}Error: ClamAV daemon not running on localhost:3310${NC}"
            echo "Start ClamAV with: sudo systemctl start clamav-daemon"
            exit 1
        fi

        echo -e "${GREEN}ClamAV detected on localhost:3310${NC}"

        # Set environment
        export CLAMAV_ADDRESS=localhost:3310
        export CLAMAV_TIMEOUT=60
        export TEST_QUARANTINE_DIR=/tmp/test-quarantine

        # Run tests against the package (collectively)
        echo ""
        echo -e "${YELLOW}Running tests...${NC}"
        cd "$PROJECT_ROOT"
        go test $TEST_FLAGS ./internal/security
        ;;

    short)
        echo -e "${YELLOW}Mode: Short (no ClamAV integration)${NC}"

        # Run tests in short mode
        echo ""
        echo -e "${YELLOW}Running tests...${NC}"
        cd "$PROJECT_ROOT"
        go test -short $TEST_FLAGS ./internal/security/...
        ;;

    *)
        echo -e "${RED}Unknown mode: $RUN_MODE${NC}"
        usage
        exit 1
        ;;
esac

# Run benchmarks if requested
if [ "$BENCHMARK" = true ]; then
    echo ""
    echo -e "${YELLOW}Running benchmarks...${NC}"
    cd "$PROJECT_ROOT"
    go test -bench=. -benchmem ./internal/security/
fi

# Generate coverage report if requested
if [ "$COVERAGE" = true ]; then
    echo ""
    echo -e "${YELLOW}Generating coverage report...${NC}"
    cd "$PROJECT_ROOT"
    go tool cover -html=coverage.out -o coverage.html
    echo -e "${GREEN}Coverage report: coverage.html${NC}"
fi

# Success
echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}  All tests passed!${NC}"
echo -e "${GREEN}========================================${NC}"
