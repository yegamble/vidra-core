#!/usr/bin/env bash
# Usage: iota-node-health.sh [host] [port]
# Checks IOTA Rebased node health via JSON-RPC.
# Exits 0 on success, 1 on failure.
HOST="${1:-localhost}"
PORT="${2:-14265}"
URL="http://${HOST}:${PORT}"

RESPONSE=$(curl -sf -X POST -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"iota_getLatestCheckpointSequenceNumber","params":[]}' \
  "${URL}" 2>&1) || { echo "ERROR: Cannot reach IOTA node at ${URL}"; exit 1; }

SEQ=$(echo "${RESPONSE}" | grep -o '"result":"[^"]*"' | cut -d'"' -f4)
if [ -z "${SEQ}" ]; then
    echo "ERROR: Unexpected response from IOTA node: ${RESPONSE}"
    exit 1
fi
echo "OK: IOTA node at ${URL} is healthy (latest checkpoint: ${SEQ})"
exit 0
