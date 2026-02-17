#!/usr/bin/env bash
# Usage: iota-testnet-up.sh [--help] [--detach]
# Starts the IOTA Rebased testnet fullnode via Docker Compose.
# On first run, the entrypoint downloads the genesis snapshot (~minutes to sync).
set -e

if [ "${1}" = "--help" ] || [ "${1}" = "-h" ]; then
    echo "Usage: $0 [--detach]"
    echo "  --detach   Run node in background (docker compose up -d)"
    echo ""
    echo "Starts the IOTA Rebased testnet fullnode."
    echo "JSON-RPC available at: http://localhost:14265"
    echo "First-boot note: genesis snapshot download + sync may take several minutes."
    exit 0
fi

DETACH_FLAG=""
[ "${1}" = "--detach" ] && DETACH_FLAG="-d"

echo "Starting IOTA testnet fullnode..."
IOTA_NETWORK=testnet docker compose --profile iota up iota-node ${DETACH_FLAG}
