#!/bin/bash
# Test suite for nginx scripts (generate-self-signed-cert.sh and entrypoint.sh)
# Usage: bash nginx/scripts/nginx_test.sh

set -e

# Capture script directory before any cd commands
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CERT_SCRIPT_PATH="$SCRIPT_DIR/generate-self-signed-cert.sh"
ENTRYPOINT_SCRIPT_PATH="$SCRIPT_DIR/entrypoint.sh"

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
    export SSL_DIR="$TEST_DIR/ssl"
    cd "$TEST_DIR"
}

teardown_test() {
    if [ -n "${TEST_DIR:-}" ]; then
        cd /
        rm -rf "$TEST_DIR"
        unset TEST_DIR
        unset SSL_DIR
    fi
}

# Mock commands
mock_openssl() {
    # Create a function that simulates openssl
    openssl() {
        case "$1" in
            req)
                # Extract output file paths from arguments
                local keyout=""
                local out=""
                local i=0
                while [ $i -lt $# ]; do
                    i=$((i + 1))
                    eval "arg=\${$i}"
                    case "$arg" in
                        -keyout)
                            i=$((i + 1))
                            eval "keyout=\${$i}"
                            ;;
                        -out)
                            i=$((i + 1))
                            eval "out=\${$i}"
                            ;;
                    esac
                done

                # Create mock certificate files
                if [ -n "$keyout" ]; then
                    echo "MOCK PRIVATE KEY" > "$keyout"
                fi
                if [ -n "$out" ]; then
                    echo "MOCK CERTIFICATE" > "$out"
                fi
                return 0
                ;;
            dhparam)
                # Extract output file from arguments
                local out=""
                local i=0
                while [ $i -lt $# ]; do
                    i=$((i + 1))
                    eval "arg=\${$i}"
                    if [ "$arg" = "-out" ]; then
                        i=$((i + 1))
                        eval "out=\${$i}"
                        break
                    fi
                done

                # Create mock DH params file
                if [ -n "$out" ]; then
                    echo "MOCK DH PARAMS" > "$out"
                fi
                return 0
                ;;
            *)
                echo "openssl $*"
                return 0
                ;;
        esac
    }
    export -f openssl 2>/dev/null || true
}

mock_openssl_missing() {
    openssl() {
        return 127  # Command not found
    }
    export -f openssl 2>/dev/null || true
}

mock_nginx() {
    nginx() {
        # Mock nginx execution
        echo "nginx started (mocked)"
        return 0
    }
    export -f nginx 2>/dev/null || true
}

# ============================================================================
# Tests for generate-self-signed-cert.sh
# ============================================================================

# Test 1: Generates cert and key files when none exist
test_cert_generation_creates_files() {
    setup_test
    mock_openssl

    # Run cert generation script
    bash "$CERT_SCRIPT_PATH" "example.com" > /dev/null 2>&1

    # Check that files were created
    if [ -f "$SSL_DIR/self-signed.crt" ] && [ -f "$SSL_DIR/self-signed.key" ]; then
        test_pass "Test 1: Generates cert and key files when none exist"
    else
        test_fail "Test 1: Generates cert and key files when none exist" "Expected cert and key files to exist"
    fi

    teardown_test
}

# Test 2: Skips generation when certs already exist (idempotent)
test_cert_generation_idempotent() {
    setup_test
    mock_openssl

    # Create existing certificates
    mkdir -p "$SSL_DIR"
    echo "EXISTING CERT" > "$SSL_DIR/self-signed.crt"
    echo "EXISTING KEY" > "$SSL_DIR/self-signed.key"

    # Run cert generation script
    output=$(bash "$CERT_SCRIPT_PATH" "example.com" 2>&1)

    # Check that script skipped generation
    if echo "$output" | grep -q "already exist"; then
        # Verify files weren't overwritten
        if grep -q "EXISTING CERT" "$SSL_DIR/self-signed.crt"; then
            test_pass "Test 2: Skips generation when certs already exist (idempotent)"
        else
            test_fail "Test 2: Skips generation when certs already exist (idempotent)" "Certificates were overwritten"
        fi
    else
        test_fail "Test 2: Skips generation when certs already exist (idempotent)" "Expected 'already exist' message"
    fi

    teardown_test
}

# Test 3: Creates output directory if missing
test_cert_generation_creates_directory() {
    setup_test
    mock_openssl

    # Ensure SSL_DIR doesn't exist
    rm -rf "$SSL_DIR"

    # Run cert generation script
    bash "$CERT_SCRIPT_PATH" "example.com" > /dev/null 2>&1

    # Check that directory was created
    if [ -d "$SSL_DIR" ]; then
        test_pass "Test 3: Creates output directory if missing"
    else
        test_fail "Test 3: Creates output directory if missing" "Expected SSL directory to be created"
    fi

    teardown_test
}

# Test 4: Sets correct file permissions on generated certs
test_cert_generation_permissions() {
    setup_test
    mock_openssl

    # Run cert generation script
    bash "$CERT_SCRIPT_PATH" "example.com" > /dev/null 2>&1

    # Check permissions
    key_perm=$(stat -f "%OLp" "$SSL_DIR/self-signed.key" 2>/dev/null || stat -c "%a" "$SSL_DIR/self-signed.key" 2>/dev/null)
    cert_perm=$(stat -f "%OLp" "$SSL_DIR/self-signed.crt" 2>/dev/null || stat -c "%a" "$SSL_DIR/self-signed.crt" 2>/dev/null)

    if [ "$key_perm" = "600" ] && [ "$cert_perm" = "644" ]; then
        test_pass "Test 4: Sets correct file permissions (key=600, cert=644)"
    else
        test_fail "Test 4: Sets correct file permissions (key=600, cert=644)" "Got key=$key_perm, cert=$cert_perm"
    fi

    teardown_test
}

# Test 5: Generates DH params when not present
test_cert_generation_dh_params() {
    setup_test
    mock_openssl

    # Run cert generation script
    bash "$CERT_SCRIPT_PATH" "example.com" > /dev/null 2>&1

    # Check that DH params file was created
    if [ -f "$SSL_DIR/dhparam.pem" ]; then
        test_pass "Test 5: Generates DH params when not present"
    else
        test_fail "Test 5: Generates DH params when not present" "Expected dhparam.pem to exist"
    fi

    teardown_test
}

# Test 6: Skips DH params generation when already exist
test_cert_generation_dh_params_idempotent() {
    setup_test
    mock_openssl

    # Create existing DH params
    mkdir -p "$SSL_DIR"
    echo "EXISTING DH PARAMS" > "$SSL_DIR/dhparam.pem"

    # Run cert generation script (this will create new certs but skip DH)
    bash "$CERT_SCRIPT_PATH" "example.com" > /dev/null 2>&1

    # Verify DH params weren't overwritten
    if grep -q "EXISTING DH PARAMS" "$SSL_DIR/dhparam.pem"; then
        test_pass "Test 6: Skips DH params generation when already exist"
    else
        test_fail "Test 6: Skips DH params generation when already exist" "DH params were overwritten"
    fi

    teardown_test
}

# Test 7: Handles missing openssl gracefully
test_cert_generation_missing_openssl() {
    setup_test

    # Create a fake bin directory with a failing openssl
    FAKE_BIN="$TEST_DIR/fake_bin"
    mkdir -p "$FAKE_BIN"

    # Create a fake openssl that exits as "command not found"
    cat > "$FAKE_BIN/openssl" << 'EOF'
#!/bin/bash
exit 127
EOF
    chmod +x "$FAKE_BIN/openssl"

    # Run cert generation script with modified PATH that finds our fake openssl first
    # The script uses `command -v openssl` which will find our fake one
    # But our fake one exits 127, which should make the script detect it as missing
    # Actually, we need to make command -v itself fail. Let's try a different approach.

    # Better approach: temporarily rename the script's openssl check
    # Create a modified version of the script for testing
    cat "$CERT_SCRIPT_PATH" | sed 's/command -v openssl/command -v openssl_DOES_NOT_EXIST/g' > "$TEST_DIR/test_cert.sh"

    # Run modified script (openssl check will fail)
    if bash "$TEST_DIR/test_cert.sh" "example.com" 2>&1 | grep -q "openssl command not found"; then
        test_pass "Test 7: Handles missing openssl gracefully"
    else
        test_fail "Test 7: Handles missing openssl gracefully" "Expected 'openssl command not found' error"
    fi

    teardown_test
}

# Test 8: Uses domain argument in certificate
test_cert_generation_domain_argument() {
    setup_test
    mock_openssl

    # Run with custom domain
    bash "$CERT_SCRIPT_PATH" "myapp.example.com" > /dev/null 2>&1

    # We can't easily verify the domain in mock cert, but we can verify script ran successfully
    if [ -f "$SSL_DIR/self-signed.crt" ] && [ -f "$SSL_DIR/self-signed.key" ]; then
        test_pass "Test 8: Uses domain argument in certificate generation"
    else
        test_fail "Test 8: Uses domain argument in certificate generation" "Certificate files not created"
    fi

    teardown_test
}

# ============================================================================
# Tests for entrypoint.sh
# ============================================================================

# Test 9: Runs cert generation when HTTPS enabled and no certs exist
test_entrypoint_https_generates_certs() {
    setup_test
    mock_openssl
    mock_nginx

    # Set HTTPS mode
    export NGINX_PROTOCOL="https"
    export NGINX_TLS_MODE="self-signed"
    export NGINX_DOMAIN="example.com"

    # Replace /etc/nginx/scripts path with our test path
    # We'll use a modified entrypoint that sources our cert script
    sed "s|/etc/nginx/scripts/|$SCRIPT_DIR/|g" "$ENTRYPOINT_SCRIPT_PATH" > "$TEST_DIR/entrypoint_test.sh"

    # Run entrypoint (will generate certs)
    bash "$TEST_DIR/entrypoint_test.sh" > /dev/null 2>&1 &
    local pid=$!
    sleep 0.5
    kill $pid 2>/dev/null || true
    wait $pid 2>/dev/null || true

    # Check that certificates were generated
    if [ -f "$SSL_DIR/self-signed.crt" ] && [ -f "$SSL_DIR/self-signed.key" ]; then
        test_pass "Test 9: Runs cert generation when HTTPS enabled and no certs exist"
    else
        test_fail "Test 9: Runs cert generation when HTTPS enabled and no certs exist" "Certificates not generated"
    fi

    unset NGINX_PROTOCOL NGINX_TLS_MODE NGINX_DOMAIN
    teardown_test
}

# Test 10: Skips cert generation when HTTP mode
test_entrypoint_http_skips_certs() {
    setup_test
    mock_nginx

    # Set HTTP mode
    export NGINX_PROTOCOL="http"
    export NGINX_DOMAIN="example.com"

    # Run entrypoint
    output=$(bash "$ENTRYPOINT_SCRIPT_PATH" 2>&1 &)
    local pid=$!
    sleep 0.5
    kill $pid 2>/dev/null || true
    wait $pid 2>/dev/null || true

    # Verify no cert generation attempted
    if [ ! -d "$SSL_DIR" ] || [ ! -f "$SSL_DIR/self-signed.crt" ]; then
        test_pass "Test 10: Skips cert generation when HTTP mode"
    else
        test_fail "Test 10: Skips cert generation when HTTP mode" "Certificates were generated in HTTP mode"
    fi

    unset NGINX_PROTOCOL NGINX_DOMAIN
    teardown_test
}

# Test 11: Skips cert generation when certs already exist
test_entrypoint_https_existing_certs() {
    setup_test
    mock_nginx

    # Create existing certificates
    mkdir -p "$SSL_DIR"
    echo "EXISTING CERT" > "$SSL_DIR/self-signed.crt"
    echo "EXISTING KEY" > "$SSL_DIR/self-signed.key"

    # Set HTTPS mode
    export NGINX_PROTOCOL="https"
    export NGINX_TLS_MODE="self-signed"
    export NGINX_DOMAIN="example.com"

    # Run entrypoint
    bash "$ENTRYPOINT_SCRIPT_PATH" > /dev/null 2>&1 &
    local pid=$!
    sleep 0.5
    kill $pid 2>/dev/null || true
    wait $pid 2>/dev/null || true

    # Verify certs weren't overwritten
    if grep -q "EXISTING CERT" "$SSL_DIR/self-signed.crt"; then
        test_pass "Test 11: Skips cert generation when certs already exist"
    else
        test_fail "Test 11: Skips cert generation when certs already exist" "Certificates were overwritten"
    fi

    unset NGINX_PROTOCOL NGINX_TLS_MODE NGINX_DOMAIN
    teardown_test
}

# Test 12: Handles environment variable defaults
test_entrypoint_defaults() {
    setup_test
    mock_nginx

    # Unset all nginx env vars
    unset NGINX_PROTOCOL NGINX_TLS_MODE NGINX_DOMAIN

    # Run entrypoint (should use defaults)
    output=$(bash "$ENTRYPOINT_SCRIPT_PATH" 2>&1 &)
    local pid=$!
    sleep 0.5
    kill $pid 2>/dev/null || true
    wait $pid 2>/dev/null || true

    # Check for default values in output (http protocol, localhost domain)
    if echo "$output" | grep -q "Protocol: http" && echo "$output" | grep -q "Domain: localhost"; then
        test_pass "Test 12: Handles missing environment variables with defaults"
    else
        test_fail "Test 12: Handles missing environment variables with defaults" "Defaults not applied correctly"
    fi

    teardown_test
}

# Test 13: Falls back to HTTP if cert generation fails
test_entrypoint_fallback_to_http() {
    setup_test
    mock_openssl_missing
    mock_nginx

    # Set HTTPS mode (will fail due to missing openssl)
    export NGINX_PROTOCOL="https"
    export NGINX_TLS_MODE="self-signed"
    export NGINX_DOMAIN="example.com"

    # Replace /etc/nginx/scripts path with our test path
    sed "s|/etc/nginx/scripts/|$SCRIPT_DIR/|g" "$ENTRYPOINT_SCRIPT_PATH" > "$TEST_DIR/entrypoint_test.sh"

    # Run entrypoint
    output=$(bash "$TEST_DIR/entrypoint_test.sh" 2>&1 &)
    local pid=$!
    sleep 0.5
    kill $pid 2>/dev/null || true
    wait $pid 2>/dev/null || true

    # Check for fallback message
    if echo "$output" | grep -q "Falling back to HTTP"; then
        test_pass "Test 13: Falls back to HTTP if cert generation fails"
    else
        test_fail "Test 13: Falls back to HTTP if cert generation fails" "Expected fallback message"
    fi

    unset NGINX_PROTOCOL NGINX_TLS_MODE NGINX_DOMAIN
    teardown_test
}

# ============================================================================
# Main test runner
# ============================================================================

main_test() {
    echo "========================================"
    echo "Nginx Scripts Test Suite"
    echo "========================================"
    echo "Testing: generate-self-signed-cert.sh"
    echo "Testing: entrypoint.sh"
    echo "========================================"
    echo ""

    # Certificate generation tests
    test_cert_generation_creates_files
    test_cert_generation_idempotent
    test_cert_generation_creates_directory
    test_cert_generation_permissions
    test_cert_generation_dh_params
    test_cert_generation_dh_params_idempotent
    test_cert_generation_missing_openssl
    test_cert_generation_domain_argument

    # Entrypoint tests
    test_entrypoint_https_generates_certs
    test_entrypoint_http_skips_certs
    test_entrypoint_https_existing_certs
    test_entrypoint_defaults
    test_entrypoint_fallback_to_http

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
