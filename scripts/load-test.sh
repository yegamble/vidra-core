#!/bin/bash
# scripts/load-test.sh
# Runs k6 load tests using Docker.

set -e
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

# Default URL
BASE_URL="${1:-http://localhost:8080}"
echo "Running load test against $BASE_URL..."

# Check if docker is available
if ! command -v docker &> /dev/null; then
    echo "Docker is required to run k6."
    exit 1
fi

# Run k6
docker run --rm -i \
    -v "$PROJECT_ROOT/tests/load:/scripts" \
    -e BASE_URL="$BASE_URL" \
    grafana/k6 run /scripts/k6-script.js

echo "Load test complete."
