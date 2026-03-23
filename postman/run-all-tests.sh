#!/bin/sh
#
# Run Vidra Core Postman test collections in a stateful sequence.
#
# Usage: ./run-all-tests.sh [environment_file]
#

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$SCRIPT_DIR"

ENV_FILE=${1:-"$SCRIPT_DIR/vidra.local.postman_environment.json"}
if [ ! -f "$ENV_FILE" ] && [ -f "$SCRIPT_DIR/$ENV_FILE" ]; then
  ENV_FILE="$SCRIPT_DIR/$ENV_FILE"
fi

if [ ! -f "$ENV_FILE" ]; then
  echo "Error: environment file '$ENV_FILE' not found"
  exit 1
fi

WORK_ENV=$(mktemp "${TMPDIR:-/tmp}/vidra-postman-env.XXXXXX")
cp "$ENV_FILE" "$WORK_ENV"
trap 'rm -f "$WORK_ENV"' EXIT INT TERM

echo "========================================="
echo "Running Vidra Core API Test Collections"
echo "Seed environment: $ENV_FILE"
echo "Working environment: $WORK_ENV"
echo "========================================="
echo ""

# Collections here are chosen to exercise the PeerTube-compatible surface plus
# Vidra Core extensions that are runnable in the test profile.
COLLECTIONS="
vidra-auth.postman_collection.json
vidra-videos.postman_collection.json
vidra-uploads.postman_collection.json
vidra-channels.postman_collection.json
vidra-social.postman_collection.json
vidra-playlists.postman_collection.json
vidra-instance-config.postman_collection.json
vidra-imports.postman_collection.json
vidra-peertube-canonical.postman_collection.json
vidra-feeds.postman_collection.json
vidra-blocklist.postman_collection.json
vidra-moderation.postman_collection.json
vidra-notifications.postman_collection.json
vidra-livestreaming.postman_collection.json
vidra-federation.postman_collection.json
vidra-secure-messaging.postman_collection.json
vidra-ipfs.postman_collection.json
vidra-runners.postman_collection.json
vidra-plugins.postman_collection.json
vidra-payments.postman_collection.json
vidra-import-lifecycle.postman_collection.json
vidra-atproto.postman_collection.json
vidra-chapters-blacklist.postman_collection.json
vidra-analytics.postman_collection.json
vidra-encoding-jobs.postman_collection.json
vidra-captions.postman_collection.json
vidra-2fa.postman_collection.json
vidra-chat.postman_collection.json
vidra-redundancy.postman_collection.json
vidra-watched-words.postman_collection.json
vidra-video-passwords.postman_collection.json
vidra-user-import-export.postman_collection.json
vidra-channel-sync.postman_collection.json
vidra-player-settings.postman_collection.json
vidra-admin-debug.postman_collection.json
vidra-video-studio.postman_collection.json
vidra-migration-etl.postman_collection.json
vidra-e2e-auth-flow.postman_collection.json
vidra-e2e-video-lifecycle.postman_collection.json
vidra-e2e-payment-flow.postman_collection.json
vidra-frontend-api-gaps.postman_collection.json
vidra-registration-edge-cases.postman_collection.json
vidra-edge-cases-security.postman_collection.json
vidra-virus-scanner-tests.postman_collection.json
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
