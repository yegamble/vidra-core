#!/bin/bash

# Test script to verify video upload validation

echo "Starting test environment..."
cd "$(dirname "$0")/.."

# Ensure services are running
COMPOSE_PROJECT_NAME=athena-test docker compose -f docker-compose.test.yml up -d postgres-test redis-test ipfs-test app-test > /dev/null 2>&1

echo "Waiting for services..."
sleep 3

echo "Testing video upload validation..."

# First register a user and get token
REGISTER_RESPONSE=$(docker run --rm --network athena-test_test-network \
  curlimages/curl:latest \
  -s -X POST http://app-test:8080/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "username": "test_validation_user",
    "email": "validation@test.com",
    "password": "TestPass123!"
  }')

# Extract token from response
ACCESS_TOKEN=$(echo "$REGISTER_RESPONSE" | grep -o '"access_token":"[^"]*' | cut -d'"' -f4)

if [ -z "$ACCESS_TOKEN" ]; then
  echo "Failed to get access token"
  exit 1
fi

echo "Got access token for testing..."

# Test with invalid MP3 file
echo ""
echo "Testing with MP3 file (should fail)..."
MP3_RESPONSE=$(docker run --rm --network athena-test_test-network \
  -v "$(pwd)/postman/test-files:/test-files" \
  curlimages/curl:latest \
  -s -X POST http://app-test:8080/api/v1/videos/upload \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -F "video=@/test-files/malicious/audio-file.mp3" \
  -F "title=Invalid Audio Upload" \
  -F "description=This should fail" \
  -F "privacy=public")

echo "MP3 upload response: $MP3_RESPONSE"
if echo "$MP3_RESPONSE" | grep -q "error"; then
  echo "✅ MP3 correctly rejected"
else
  echo "❌ MP3 should have been rejected"
fi

# Test with PDF document
echo ""
echo "Testing with PDF file (should fail)..."
PDF_RESPONSE=$(docker run --rm --network athena-test_test-network \
  -v "$(pwd)/postman/test-files:/test-files" \
  curlimages/curl:latest \
  -s -X POST http://app-test:8080/api/v1/videos/upload \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -F "video=@/test-files/malicious/document.pdf" \
  -F "title=Invalid PDF Upload" \
  -F "description=This should fail" \
  -F "privacy=public")

echo "PDF upload response: $PDF_RESPONSE"
if echo "$PDF_RESPONSE" | grep -q "error"; then
  echo "✅ PDF correctly rejected"
else
  echo "❌ PDF should have been rejected"
fi

# Test with valid MP4
echo ""
echo "Testing with valid MP4 (should succeed)..."
MP4_RESPONSE=$(docker run --rm --network athena-test_test-network \
  -v "$(pwd)/postman/test-files:/test-files" \
  curlimages/curl:latest \
  -s -X POST http://app-test:8080/api/v1/videos/upload \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -F "video=@/test-files/videos/test-video.mp4" \
  -F "title=Valid MP4 Upload" \
  -F "description=This should succeed" \
  -F "privacy=public")

echo "MP4 upload response: $MP4_RESPONSE"
if echo "$MP4_RESPONSE" | grep -q '"id"'; then
  echo "✅ MP4 correctly accepted"
else
  echo "❌ MP4 should have been accepted"
fi

echo ""
echo "Cleaning up..."
COMPOSE_PROJECT_NAME=athena-test docker compose -f docker-compose.test.yml down -v > /dev/null 2>&1

echo "Validation tests complete!"
