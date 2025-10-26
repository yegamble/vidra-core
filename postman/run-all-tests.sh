#!/bin/bash
#
# Run all Postman test collections
#
# Usage: ./run-all-tests.sh [environment_file]
#

set -e

# Use provided environment file or default
ENV_FILE="${1:-athena.local.postman_environment.json}"

if [ ! -f "$ENV_FILE" ]; then
  echo "Error: Environment file '$ENV_FILE' not found"
  exit 1
fi

echo "========================================="
echo "Running Athena API Test Collections"
echo "Environment: $ENV_FILE"
echo "========================================="
echo ""

# Define collections in execution order
collections=(
  "athena-auth.postman_collection.json"
  "athena-uploads.postman_collection.json"
  "athena-analytics.postman_collection.json"
  "athena-imports.postman_collection.json"
)

# Track results
total_tests=0
passed_tests=0
failed_tests=0
failed_collections=()

# Run each collection
for collection in "${collections[@]}"; do
  if [ ! -f "$collection" ]; then
    echo "⚠️  Warning: $collection not found, skipping..."
    echo ""
    continue
  fi

  echo "📋 Running $collection..."
  echo "---"

  # Run newman and capture output
  if newman run "$collection" \
    -e "$ENV_FILE" \
    --reporters cli,json \
    --reporter-json-export "results-${collection%.json}.json" \
    --color on; then
    echo "✅ $collection completed successfully"
  else
    echo "❌ $collection failed"
    failed_collections+=("$collection")
  fi

  echo ""
  echo "========================================="
  echo ""
done

# Summary
echo "📊 Test Execution Summary"
echo "========================================="
echo "Collections run: ${#collections[@]}"
echo "Successful: $((${#collections[@]} - ${#failed_collections[@]}))"
echo "Failed: ${#failed_collections[@]}"

if [ ${#failed_collections[@]} -gt 0 ]; then
  echo ""
  echo "Failed collections:"
  for fc in "${failed_collections[@]}"; do
    echo "  - $fc"
  done
  echo ""
  exit 1
else
  echo ""
  echo "🎉 All collections passed!"
  exit 0
fi
