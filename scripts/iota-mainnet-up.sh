#!/usr/bin/env bash
# Usage: iota-mainnet-up.sh [--help] [--detach]
# Starts the IOTA Rebased mainnet fullnode via Docker Compose.
# WARNING: Mainnet initial sync can take several hours. Use a snapshot for faster sync.
set -e

if [ "${1}" = "--help" ] || [ "${1}" = "-h" ]; then
    echo "Usage: $0 [--detach]"
    echo "  --detach   Run node in background (docker compose up -d)"
    echo ""
    echo "Starts the IOTA Rebased mainnet fullnode."
    echo "JSON-RPC available at: http://localhost:14265"
    echo "WARNING: Initial sync from genesis can take several hours."
    exit 0
fi

DETACH_FLAG=""
[ "${1}" = "--detach" ] && DETACH_FLAG="-d"

echo "Starting IOTA mainnet fullnode (this may take hours to sync)..."
IOTA_NETWORK=mainnet docker compose --profile iota up iota-node ${DETACH_FLAG}
