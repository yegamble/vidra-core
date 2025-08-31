#!/bin/bash

# Script to run Postman tests with proper authentication flow
# This ensures Register/Login runs first to set up tokens for video tests

cd "$(dirname "$0")/.."  # Go to project root

echo "Starting test environment..."
COMPOSE_PROJECT_NAME=athena-test docker compose -f docker-compose.test.yml down -v > /dev/null 2>&1
COMPOSE_PROJECT_NAME=athena-test docker compose -f docker-compose.test.yml up -d postgres-test redis-test ipfs-test app-test

echo "Waiting for services to be ready..."
sleep 5

echo "Running Postman tests - Auth first, then Videos..."
docker run --rm \
  --network athena-test_test-network \
  -v ./postman:/etc/newman \
  postman/newman:alpine \
  run /etc/newman/athena-auth.postman_collection.json \
  --env-var "baseUrl=http://app-test:8080" \
  --reporters cli,junit \
  --reporter-junit-export /etc/newman/newman-results.xml

echo "Cleaning up test environment..."
COMPOSE_PROJECT_NAME=athena-test docker compose -f docker-compose.test.yml down -v

echo "Tests complete!"