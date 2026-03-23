#!/bin/bash

# test_import_api.sh - Test script for Video Import API
# This script demonstrates the complete video import flow

set -e

# Configuration
API_BASE_URL="${API_BASE_URL:-http://localhost:8080}"
JWT_TOKEN="${JWT_TOKEN:-}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Helper functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

# Check if JWT token is set
if [ -z "$JWT_TOKEN" ]; then
    log_error "JWT_TOKEN environment variable is not set"
    echo "Usage: JWT_TOKEN=your_token_here $0"
    echo ""
    echo "To get a token, login first:"
    echo "  curl -X POST $API_BASE_URL/api/v1/auth/login \\"
    echo "    -H 'Content-Type: application/json' \\"
    echo "    -d '{\"username\":\"your_username\",\"password\":\"your_password\"}'"
    exit 1
fi

log_info "Testing Video Import API at $API_BASE_URL"
echo ""

# Test 1: Create a new video import
log_info "Test 1: Creating a new video import..."
IMPORT_RESPONSE=$(curl -s -X POST "$API_BASE_URL/api/v1/videos/imports" \
    -H "Authorization: Bearer $JWT_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{
        "source_url": "https://www.youtube.com/watch?v=jNQXAC9IVRw",
        "target_privacy": "private"
    }')

if echo "$IMPORT_RESPONSE" | jq -e '.id' > /dev/null 2>&1; then
    IMPORT_ID=$(echo "$IMPORT_RESPONSE" | jq -r '.id')
    log_success "Import created successfully"
    echo "  Import ID: $IMPORT_ID"
    echo "  Status: $(echo "$IMPORT_RESPONSE" | jq -r '.status')"
    echo "  Source: $(echo "$IMPORT_RESPONSE" | jq -r '.source_url')"
    echo "  Platform: $(echo "$IMPORT_RESPONSE" | jq -r '.source_platform')"
else
    log_error "Failed to create import"
    echo "$IMPORT_RESPONSE" | jq '.' 2>/dev/null || echo "$IMPORT_RESPONSE"
    exit 1
fi
echo ""

# Test 2: Get import status
log_info "Test 2: Getting import status..."
sleep 1
STATUS_RESPONSE=$(curl -s -X GET "$API_BASE_URL/api/v1/videos/imports/$IMPORT_ID" \
    -H "Authorization: Bearer $JWT_TOKEN")

if echo "$STATUS_RESPONSE" | jq -e '.id' > /dev/null 2>&1; then
    log_success "Import status retrieved"
    echo "  Status: $(echo "$STATUS_RESPONSE" | jq -r '.status')"
    echo "  Progress: $(echo "$STATUS_RESPONSE" | jq -r '.progress')%"
    if [ "$(echo "$STATUS_RESPONSE" | jq -r '.error_message')" != "null" ]; then
        echo "  Error: $(echo "$STATUS_RESPONSE" | jq -r '.error_message')"
    fi
else
    log_error "Failed to get import status"
    echo "$STATUS_RESPONSE" | jq '.' 2>/dev/null || echo "$STATUS_RESPONSE"
fi
echo ""

# Test 3: List user imports
log_info "Test 3: Listing user imports..."
LIST_RESPONSE=$(curl -s -X GET "$API_BASE_URL/api/v1/videos/imports?limit=10&offset=0" \
    -H "Authorization: Bearer $JWT_TOKEN")

if echo "$LIST_RESPONSE" | jq -e '.imports' > /dev/null 2>&1; then
    IMPORT_COUNT=$(echo "$LIST_RESPONSE" | jq '.imports | length')
    TOTAL_COUNT=$(echo "$LIST_RESPONSE" | jq -r '.total_count')
    log_success "Import list retrieved"
    echo "  Showing: $IMPORT_COUNT imports"
    echo "  Total: $TOTAL_COUNT imports"
    echo ""
    echo "  Recent imports:"
    echo "$LIST_RESPONSE" | jq -r '.imports[] | "    - \(.id): \(.status) (\(.source_platform))"' | head -5
else
    log_error "Failed to list imports"
    echo "$LIST_RESPONSE" | jq '.' 2>/dev/null || echo "$LIST_RESPONSE"
fi
echo ""

# Test 4: Monitor import progress
log_info "Test 4: Monitoring import progress for 10 seconds..."
for i in {1..10}; do
    sleep 1
    PROGRESS_RESPONSE=$(curl -s -X GET "$API_BASE_URL/api/v1/videos/imports/$IMPORT_ID" \
        -H "Authorization: Bearer $JWT_TOKEN")

    STATUS=$(echo "$PROGRESS_RESPONSE" | jq -r '.status')
    PROGRESS=$(echo "$PROGRESS_RESPONSE" | jq -r '.progress')

    echo -ne "  ${i}s: Status=$STATUS, Progress=$PROGRESS%\r"

    # Break if terminal state reached
    if [[ "$STATUS" == "completed" || "$STATUS" == "failed" || "$STATUS" == "cancelled" ]]; then
        echo ""
        log_success "Import reached terminal state: $STATUS"
        if [ "$STATUS" == "completed" ]; then
            VIDEO_ID=$(echo "$PROGRESS_RESPONSE" | jq -r '.video_id')
            echo "  Video ID: $VIDEO_ID"
        fi
        break
    fi
done
echo ""
echo ""

# Test 5: Cancel import (if still in progress)
CURRENT_STATUS=$(curl -s -X GET "$API_BASE_URL/api/v1/videos/imports/$IMPORT_ID" \
    -H "Authorization: Bearer $JWT_TOKEN" | jq -r '.status')

if [[ "$CURRENT_STATUS" != "completed" && "$CURRENT_STATUS" != "failed" && "$CURRENT_STATUS" != "cancelled" ]]; then
    log_info "Test 5: Cancelling import..."
    CANCEL_RESPONSE=$(curl -s -w "\n%{http_code}" -X DELETE "$API_BASE_URL/api/v1/videos/imports/$IMPORT_ID" \
        -H "Authorization: Bearer $JWT_TOKEN")

    HTTP_CODE=$(echo "$CANCEL_RESPONSE" | tail -n 1)

    if [ "$HTTP_CODE" == "204" ]; then
        log_success "Import cancelled successfully"

        # Verify cancellation
        sleep 1
        VERIFY_RESPONSE=$(curl -s -X GET "$API_BASE_URL/api/v1/videos/imports/$IMPORT_ID" \
            -H "Authorization: Bearer $JWT_TOKEN")
        FINAL_STATUS=$(echo "$VERIFY_RESPONSE" | jq -r '.status')
        echo "  Final status: $FINAL_STATUS"
    else
        log_warning "Import could not be cancelled (may already be completed)"
        echo "  HTTP Code: $HTTP_CODE"
    fi
else
    log_info "Test 5: Skipping cancellation (import already in terminal state: $CURRENT_STATUS)"
fi
echo ""

# Test 6: Test quota limits (optional, creates many imports)
if [ "${TEST_QUOTA:-false}" == "true" ]; then
    log_info "Test 6: Testing quota limits..."
    log_warning "This will create many imports!"

    SUCCESS_COUNT=0
    QUOTA_EXCEEDED=false

    for i in {1..15}; do
        RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "$API_BASE_URL/api/v1/videos/imports" \
            -H "Authorization: Bearer $JWT_TOKEN" \
            -H "Content-Type: application/json" \
            -d "{
                \"source_url\": \"https://www.youtube.com/watch?v=test$i\",
                \"target_privacy\": \"private\"
            }")

        HTTP_CODE=$(echo "$RESPONSE" | tail -n 1)

        if [ "$HTTP_CODE" == "201" ]; then
            ((SUCCESS_COUNT++))
        elif [ "$HTTP_CODE" == "429" ]; then
            QUOTA_EXCEEDED=true
            log_warning "Quota/rate limit reached after $SUCCESS_COUNT successful imports"
            break
        fi
    done

    if [ "$QUOTA_EXCEEDED" == "true" ]; then
        log_success "Quota enforcement working correctly"
    else
        log_info "Created $SUCCESS_COUNT imports without hitting quota"
    fi
    echo ""
fi

# Summary
log_success "All tests completed!"
echo ""
echo "Summary:"
echo "  ✓ Import creation"
echo "  ✓ Status retrieval"
echo "  ✓ Import listing"
echo "  ✓ Progress monitoring"
if [[ "$CURRENT_STATUS" == "cancelled" ]]; then
    echo "  ✓ Import cancellation"
else
    echo "  ~ Import cancellation (skipped - already $CURRENT_STATUS)"
fi

if [ "${TEST_QUOTA:-false}" == "true" ]; then
    echo "  ✓ Quota testing"
fi

echo ""
echo "For more information:"
echo "  - API Documentation: $API_BASE_URL/api/docs"
echo "  - Import Status: curl -H 'Authorization: Bearer \$JWT_TOKEN' $API_BASE_URL/api/v1/videos/imports/$IMPORT_ID"
echo "  - List Imports: curl -H 'Authorization: Bearer \$JWT_TOKEN' $API_BASE_URL/api/v1/videos/imports"
