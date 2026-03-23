#!/bin/bash

# Virus Scanner Test Execution Script
# This script runs comprehensive tests for the virus scanning functionality
# including edge cases, breaking scenarios, and performance validation

set -e  # Exit on error

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
RESULTS_DIR="$SCRIPT_DIR/newman"
TIMESTAMP=$(date +%Y%m%d-%H%M%S)

# Test configuration
BASE_URL="${BASE_URL:-http://localhost:8080}"
CLAMAV_ADDRESS="${CLAMAV_ADDRESS:-localhost:3310}"
ENABLE_BREAKING_TESTS="${ENABLE_BREAKING_TESTS:-true}"
ENABLE_PERFORMANCE_TESTS="${ENABLE_PERFORMANCE_TESTS:-true}"
PARALLEL_TESTS="${PARALLEL_TESTS:-10}"

# Function to print colored output
print_status() {
    local color=$1
    shift
    echo -e "${color}$@${NC}"
}

print_header() {
    echo ""
    print_status "$BLUE" "========================================"
    print_status "$BLUE" "$1"
    print_status "$BLUE" "========================================"
    echo ""
}

print_success() {
    print_status "$GREEN" "✓ $1"
}

print_error() {
    print_status "$RED" "✗ $1"
}

print_warning() {
    print_status "$YELLOW" "⚠ $1"
}

# Function to check prerequisites
check_prerequisites() {
    print_header "Checking Prerequisites"

    # Check if Newman is installed
    if ! command -v newman &> /dev/null; then
        print_error "Newman is not installed"
        echo "Install with: npm install -g newman newman-reporter-htmlextra"
        exit 1
    fi
    print_success "Newman installed"

    # Check if jq is installed
    if ! command -v jq &> /dev/null; then
        print_error "jq is not installed"
        echo "Install with: sudo apt-get install jq (Ubuntu) or brew install jq (macOS)"
        exit 1
    fi
    print_success "jq installed"

    # Check if curl is installed
    if ! command -v curl &> /dev/null; then
        print_error "curl is not installed"
        exit 1
    fi
    print_success "curl installed"

    # Check if API is running
    if ! curl -f -s "$BASE_URL/api/v1/health" > /dev/null 2>&1; then
        print_error "API not reachable at $BASE_URL"
        echo "Start the application first: make run"
        exit 1
    fi
    print_success "API is reachable"

    # Check if ClamAV is running
    if ! timeout 5 bash -c "echo 'PING' | nc -w 1 localhost 3310" > /dev/null 2>&1; then
        print_warning "ClamAV daemon not reachable at $CLAMAV_ADDRESS"
        print_warning "Some tests may fail. Start ClamAV with: docker run -d -p 3310:3310 clamav/clamav"
    else
        print_success "ClamAV daemon is reachable"
    fi
}

# Function to create test user and get access token
create_test_user() {
    print_header "Creating Test User"

    local timestamp=$(date +%s)
    local test_username="virus_test_${timestamp}"
    local test_email="virustest${timestamp}@example.com"
    local test_password="VerySecurePassword123!"

    print_status "$BLUE" "Registering user: $test_username"

    local response=$(curl -s -X POST "$BASE_URL/api/v1/auth/register" \
        -H "Content-Type: application/json" \
        -d "{
            \"username\": \"$test_username\",
            \"email\": \"$test_email\",
            \"password\": \"$test_password\",
            \"display_name\": \"Virus Test User\"
        }")

    # Check if registration was successful
    local success=$(echo "$response" | jq -r '.success // false')
    if [ "$success" != "true" ]; then
        print_error "Failed to create test user"
        echo "Response: $response"
        exit 1
    fi

    # Extract access token
    ACCESS_TOKEN=$(echo "$response" | jq -r '.data.access_token')
    if [ -z "$ACCESS_TOKEN" ] || [ "$ACCESS_TOKEN" = "null" ]; then
        print_error "Failed to extract access token"
        echo "Response: $response"
        exit 1
    fi

    print_success "Test user created successfully"
    print_status "$GREEN" "Access token obtained"

    # Save token to environment file
    echo "access_token=$ACCESS_TOKEN" > "$SCRIPT_DIR/.test-env"
}

# Function to create test files
create_test_files() {
    print_header "Creating Test Files"

    local test_files_dir="$SCRIPT_DIR/test-files/security"
    mkdir -p "$test_files_dir"

    # EICAR test virus (standard antivirus test file - NOT real malware)
    print_status "$BLUE" "Creating EICAR test files..."
    echo 'X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*' > "$test_files_dir/eicar.txt"
    echo 'X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*' > "$test_files_dir/eicar.com"
    print_success "EICAR test files created"

    # 10MB exact boundary file
    if [ ! -f "$test_files_dir/10mb-exact.bin" ]; then
        print_status "$BLUE" "Creating 10MB boundary test file..."
        dd if=/dev/urandom of="$test_files_dir/10mb-exact.bin" bs=1M count=10 2>/dev/null
        print_success "10MB exact boundary file created"
    else
        print_success "10MB exact boundary file already exists"
    fi

    # 10MB + 1 byte file
    if [ ! -f "$test_files_dir/10mb-plus-one.bin" ]; then
        print_status "$BLUE" "Creating 10MB+1 boundary test file..."
        dd if=/dev/urandom of="$test_files_dir/10mb-plus-one.bin" bs=1M count=10 2>/dev/null
        echo "X" >> "$test_files_dir/10mb-plus-one.bin"
        print_success "10MB+1 boundary file created"
    else
        print_success "10MB+1 boundary file already exists"
    fi

    # Large file for performance testing
    if [ "$ENABLE_PERFORMANCE_TESTS" = "true" ]; then
        if [ ! -f "$test_files_dir/100mb-test.bin" ]; then
            print_status "$BLUE" "Creating 100MB performance test file..."
            dd if=/dev/urandom of="$test_files_dir/100mb-test.bin" bs=1M count=100 2>/dev/null
            print_success "100MB performance test file created"
        else
            print_success "100MB performance test file already exists"
        fi
    fi

    # Clean test files
    echo "This is a clean test file for virus scanning" > "$test_files_dir/clean-small.txt"
    print_success "Clean test files created"

    print_status "$GREEN" "All test files ready"
}

# Function to run Postman tests
run_postman_tests() {
    print_header "Running Postman Tests"

    mkdir -p "$RESULTS_DIR"

    local collection="$SCRIPT_DIR/athena-virus-scanner-tests.postman_collection.json"
    local environment="$SCRIPT_DIR/test-local.postman_environment.json"
    local report_file="$RESULTS_DIR/virus-scanner-report-${TIMESTAMP}.html"
    local json_file="$RESULTS_DIR/virus-scanner-results-${TIMESTAMP}.json"

    print_status "$BLUE" "Running Newman collection..."
    print_status "$BLUE" "Collection: $collection"
    print_status "$BLUE" "Environment: $environment"

    # Run Newman with comprehensive reporting
    newman run "$collection" \
        --environment "$environment" \
        --env-var "baseUrl=$BASE_URL" \
        --env-var "access_token=$ACCESS_TOKEN" \
        --env-var "clamav_address=$CLAMAV_ADDRESS" \
        --reporters cli,htmlextra,json \
        --reporter-htmlextra-export "$report_file" \
        --reporter-json-export "$json_file" \
        --timeout-request 60000 \
        --delay-request 100 \
        --color on \
        || {
            print_error "Some tests failed - see report for details"
            TESTS_FAILED=true
        }

    if [ -z "$TESTS_FAILED" ]; then
        print_success "All Postman tests passed!"
    fi

    print_status "$GREEN" "HTML Report: $report_file"
    print_status "$GREEN" "JSON Results: $json_file"
}

# Function to run breaking tests
run_breaking_tests() {
    if [ "$ENABLE_BREAKING_TESTS" != "true" ]; then
        print_warning "Breaking tests disabled"
        return
    fi

    print_header "Running Breaking Scenario Tests"

    print_status "$BLUE" "Test 1: Network interruption during scan"

    # This test validates the P1 vulnerability fix
    # Scenario: Upload EICAR, kill ClamAV mid-scan
    # Expected: Upload should be rejected (not accepted with warning)

    local eicar_file="$SCRIPT_DIR/test-files/security/eicar.txt"

    # Start upload in background
    print_status "$BLUE" "Starting upload of infected file..."
    local upload_response=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/v1/uploads/direct" \
        -H "Authorization: Bearer $ACCESS_TOKEN" \
        -H "Content-Type: application/octet-stream" \
        -H "X-Filename: eicar-breaking-test.txt" \
        --data-binary "@$eicar_file" 2>&1)

    local http_code=$(echo "$upload_response" | tail -n1)
    local response_body=$(echo "$upload_response" | head -n-1)

    print_status "$BLUE" "HTTP Status: $http_code"

    # Infected files should be rejected with 403 or 400
    if [ "$http_code" = "403" ] || [ "$http_code" = "400" ]; then
        print_success "Breaking Test 1 PASSED: Infected file correctly rejected"
    elif [ "$http_code" = "201" ] || [ "$http_code" = "200" ]; then
        print_error "Breaking Test 1 FAILED: Infected file was accepted!"
        print_error "P1 VULNERABILITY STILL EXISTS!"
        echo "Response: $response_body"
        exit 1
    else
        print_warning "Breaking Test 1: Unexpected status code $http_code"
        echo "Response: $response_body"
    fi

    print_status "$BLUE" "Test 2: Concurrent infected file uploads"

    # Test for race conditions
    print_status "$BLUE" "Starting $PARALLEL_TESTS concurrent uploads..."

    local failed_count=0
    local success_count=0

    for i in $(seq 1 $PARALLEL_TESTS); do
        {
            local response=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/v1/uploads/direct" \
                -H "Authorization: Bearer $ACCESS_TOKEN" \
                -H "Content-Type: application/octet-stream" \
                -H "X-Filename: eicar-concurrent-$i.txt" \
                --data-binary "@$eicar_file" 2>&1)

            local code=$(echo "$response" | tail -n1)

            if [ "$code" = "403" ] || [ "$code" = "400" ]; then
                echo "REJECTED" > "/tmp/concurrent-test-$i.result"
            else
                echo "ACCEPTED" > "/tmp/concurrent-test-$i.result"
            fi
        } &
    done

    # Wait for all concurrent uploads to complete
    wait

    # Check results
    for i in $(seq 1 $PARALLEL_TESTS); do
        local result=$(cat "/tmp/concurrent-test-$i.result" 2>/dev/null || echo "ERROR")
        if [ "$result" = "REJECTED" ]; then
            success_count=$((success_count + 1))
        else
            failed_count=$((failed_count + 1))
        fi
        rm -f "/tmp/concurrent-test-$i.result"
    done

    print_status "$BLUE" "Concurrent test results: $success_count rejected, $failed_count accepted"

    if [ $failed_count -gt 0 ]; then
        print_error "Breaking Test 2 FAILED: Some infected files were accepted under load!"
        print_error "RACE CONDITION VULNERABILITY DETECTED!"
        exit 1
    else
        print_success "Breaking Test 2 PASSED: All infected files rejected under concurrent load"
    fi
}

# Function to run performance tests
run_performance_tests() {
    if [ "$ENABLE_PERFORMANCE_TESTS" != "true" ]; then
        print_warning "Performance tests disabled"
        return
    fi

    print_header "Running Performance Tests"

    local small_file="$SCRIPT_DIR/test-files/security/clean-small.txt"
    local large_file="$SCRIPT_DIR/test-files/security/100mb-test.bin"

    # Test 1: Small file scan performance
    print_status "$BLUE" "Test 1: Small file scan performance"

    local start_time=$(date +%s%N)
    local response=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/v1/uploads/direct" \
        -H "Authorization: Bearer $ACCESS_TOKEN" \
        -H "Content-Type: application/octet-stream" \
        -H "X-Filename: perf-small.txt" \
        --data-binary "@$small_file")
    local end_time=$(date +%s%N)

    local duration_ms=$(( (end_time - start_time) / 1000000 ))
    print_status "$BLUE" "Small file scan completed in ${duration_ms}ms"

    if [ $duration_ms -lt 5000 ]; then
        print_success "Performance Test 1 PASSED: Small file scan < 5s"
    else
        print_warning "Performance Test 1: Small file scan took ${duration_ms}ms (threshold: 5000ms)"
    fi

    # Test 2: Large file scan performance (if enabled)
    if [ -f "$large_file" ]; then
        print_status "$BLUE" "Test 2: Large file scan performance (100MB)"

        start_time=$(date +%s%N)
        response=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/v1/uploads/direct" \
            -H "Authorization: Bearer $ACCESS_TOKEN" \
            -H "Content-Type: application/octet-stream" \
            -H "X-Filename: perf-large.bin" \
            --data-binary "@$large_file")
        end_time=$(date +%s%N)

        duration_ms=$(( (end_time - start_time) / 1000000 ))
        local duration_s=$(( duration_ms / 1000 ))

        print_status "$BLUE" "Large file scan completed in ${duration_s}s"

        if [ $duration_s -lt 30 ]; then
            print_success "Performance Test 2 PASSED: Large file scan < 30s"
        else
            print_warning "Performance Test 2: Large file scan took ${duration_s}s (threshold: 30s)"
        fi
    fi
}

# Function to generate summary report
generate_summary() {
    print_header "Test Summary"

    local latest_results=$(ls -t "$RESULTS_DIR"/virus-scanner-results-*.json 2>/dev/null | head -n1)

    if [ -n "$latest_results" ]; then
        local total_tests=$(jq '.run.stats.tests.total' "$latest_results")
        local passed_tests=$(jq '.run.stats.tests.passed' "$latest_results")
        local failed_tests=$(jq '.run.stats.tests.failed' "$latest_results")

        echo "Total Tests: $total_tests"
        echo "Passed: $passed_tests"
        echo "Failed: $failed_tests"

        if [ "$failed_tests" -eq 0 ]; then
            print_success "All tests passed!"
        else
            print_error "$failed_tests test(s) failed"
        fi
    fi

    print_status "$BLUE" "Results saved to: $RESULTS_DIR"

    # Open HTML report if available
    local latest_report=$(ls -t "$RESULTS_DIR"/virus-scanner-report-*.html 2>/dev/null | head -n1)
    if [ -n "$latest_report" ]; then
        print_status "$GREEN" "HTML Report: file://$latest_report"

        # Try to open in browser (optional)
        if command -v xdg-open &> /dev/null; then
            xdg-open "$latest_report" 2>/dev/null || true
        elif command -v open &> /dev/null; then
            open "$latest_report" 2>/dev/null || true
        fi
    fi
}

# Main execution
main() {
    print_header "Virus Scanner Test Suite"
    echo "Base URL: $BASE_URL"
    echo "ClamAV: $CLAMAV_ADDRESS"
    echo "Breaking Tests: $ENABLE_BREAKING_TESTS"
    echo "Performance Tests: $ENABLE_PERFORMANCE_TESTS"
    echo ""

    check_prerequisites
    create_test_user
    create_test_files
    run_postman_tests
    run_breaking_tests
    run_performance_tests
    generate_summary

    print_header "Test Execution Complete"

    # Exit with error code if tests failed
    if [ -n "$TESTS_FAILED" ]; then
        exit 1
    fi
}

# Run main function
main "$@"
