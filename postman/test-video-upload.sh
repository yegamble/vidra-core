#!/bin/bash

# Quick test script to verify video upload works with proper auth

echo "Starting test environment..."
cd "$(dirname "$0")/.."

# Ensure clean state
COMPOSE_PROJECT_NAME=athena-test docker compose -f docker-compose.test.yml down -v > /dev/null 2>&1
COMPOSE_PROJECT_NAME=athena-test docker compose -f docker-compose.test.yml up -d postgres-test redis-test ipfs-test app-test

echo "Waiting for services..."
sleep 5

echo "Testing video upload with authentication..."

# First register a user and get token using docker
REGISTER_RESPONSE=$(docker run --rm --network athena-test_test-network \
  curlimages/curl:latest \
  -s -X POST http://app-test:8080/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "username": "test_video_user",
    "email": "video@test.com",
    "password": "TestPass123!"
  }')

echo "Register response: $REGISTER_RESPONSE"

# Extract token from response
ACCESS_TOKEN=$(echo "$REGISTER_RESPONSE" | grep -o '"access_token":"[^"]*' | cut -d'"' -f4)

if [ -z "$ACCESS_TOKEN" ]; then
  echo "Failed to get access token"
  exit 1
fi

echo "Got access token: ${ACCESS_TOKEN:0:20}..."

# Test video upload with the token
echo "Testing video upload..."
UPLOAD_RESPONSE=$(docker run --rm --network athena-test_test-network \
  -v "$(pwd)/postman/test-files:/test-files" \
  curlimages/curl:latest \
  -s -X POST http://app-test:8080/api/v1/videos/upload \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -F "video=@/test-files/videos/test-video.mp4" \
  -F "title=Test Video Upload" \
  -F "description=Testing video upload with auth" \
  -F "privacy=public")

echo "Upload response: $UPLOAD_RESPONSE"

# Check if upload was successful
if echo "$UPLOAD_RESPONSE" | grep -q '"id"'; then
  echo "✅ Video upload successful!"
else
  echo "❌ Video upload failed"
fi

echo "Cleaning up..."
COMPOSE_PROJECT_NAME=athena-test docker compose -f docker-compose.test.yml down -v

echo "Test complete!"