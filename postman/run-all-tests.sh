#!/bin/sh
#
# Run Athena Postman test collections in a stateful sequence.
#
# Usage: ./run-all-tests.sh [environment_file]
#

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$SCRIPT_DIR"

ENV_FILE=${1:-"$SCRIPT_DIR/athena.local.postman_environment.json"}
if [ ! -f "$ENV_FILE" ] && [ -f "$SCRIPT_DIR/$ENV_FILE" ]; then
  ENV_FILE="$SCRIPT_DIR/$ENV_FILE"
fi

if [ ! -f "$ENV_FILE" ]; then
  echo "Error: environment file '$ENV_FILE' not found"
  exit 1
fi

WORK_ENV=$(mktemp "${TMPDIR:-/tmp}/athena-postman-env.XXXXXX")
cp "$ENV_FILE" "$WORK_ENV"
trap 'rm -f "$WORK_ENV"' EXIT INT TERM

echo "========================================="
echo "Running Athena API Test Collections"
echo "Seed environment: $ENV_FILE"
echo "Working environment: $WORK_ENV"
echo "========================================="
echo ""

# Collections here are chosen to exercise the PeerTube-compatible surface plus
# Athena extensions that are runnable in the test profile.
COLLECTIONS="
athena-auth.postman_collection.json
athena-videos.postman_collection.json
athena-uploads.postman_collection.json
athena-channels.postman_collection.json
athena-instance-config.postman_collection.json
athena-imports.postman_collection.json
athena-peertube-canonical.postman_collection.json
athena-feeds.postman_collection.json
athena-blocklist.postman_collection.json
athena-notifications.postman_collection.json
athena-livestreaming.postman_collection.json
athena-federation.postman_collection.json
athena-secure-messaging.postman_collection.json
athena-ipfs.postman_collection.json
athena-runners.postman_collection.json
athena-plugins.postman_collection.json
athena-payments.postman_collection.json
athena-import-lifecycle.postman_collection.json
athena-atproto.postman_collection.json
"

collections_run=0
successful=0
failed=0
failed_collections=""

for collection in $COLLECTIONS; do
  if [ ! -f "$collection" ]; then
    echo "Warning: $collection not found, skipping"
    echo ""
    continue
  fi

  collections_run=$((collections_run + 1))
  echo "Running $collection..."
  echo "---"

  report_file="results-${collection%.json}.json"
  if newman run "$collection" \
    -e "$WORK_ENV" \
    --reporters cli,json \
    --reporter-json-export "$report_file" \
    --export-environment "$WORK_ENV" \
    --color on; then
    successful=$((successful + 1))
    echo "$collection completed successfully"
  else
    failed=$((failed + 1))
    failed_collections="$failed_collections
$collection"
    echo "$collection failed"
  fi

  echo ""
  echo "========================================="
  echo ""
done

echo "Test Execution Summary"
echo "========================================="
echo "Collections run: $collections_run"
echo "Successful: $successful"
echo "Failed: $failed"

if [ "$failed" -gt 0 ]; then
  echo ""
  echo "Failed collections:"
  printf '%s\n' "$failed_collections" | sed '/^$/d; s/^/  - /'
  echo ""
  exit 1
fi

echo ""
echo "All collections passed."
