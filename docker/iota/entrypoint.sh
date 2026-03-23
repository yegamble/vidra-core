#!/bin/sh
# IOTA Rebased fullnode entrypoint script
# Downloads genesis snapshot on first boot if not present, then starts the node.
# First-boot sync: ~minutes for testnet (with snapshot), ~hours for mainnet.
set -e

GENESIS_PATH="/opt/iota/config/genesis.blob"
TESTNET_GENESIS_URL="https://dbfiles.testnet.iota.cafe/genesis.blob"
MAINNET_GENESIS_URL="https://dbfiles.mainnet.iota.cafe/genesis.blob"
NETWORK="${IOTA_NETWORK:-testnet}"

echo "[entrypoint] IOTA Rebased fullnode starting (network: ${NETWORK})"

if [ ! -f "${GENESIS_PATH}" ]; then
    echo "[entrypoint] Genesis snapshot not found. Downloading for ${NETWORK}..."
    mkdir -p "$(dirname ${GENESIS_PATH})"

    if [ "${NETWORK}" = "mainnet" ]; then
        GENESIS_URL="${MAINNET_GENESIS_URL}"
    else
        GENESIS_URL="${TESTNET_GENESIS_URL}"
    fi

    curl -fsSL -o "${GENESIS_PATH}" "${GENESIS_URL}" || {
        echo "[entrypoint] ERROR: Failed to download genesis snapshot from ${GENESIS_URL}"
        exit 1
    }
    echo "[entrypoint] Genesis snapshot downloaded successfully."
else
    echo "[entrypoint] Genesis snapshot already present, skipping download."
fi

echo "[entrypoint] Starting IOTA fullnode..."
exec iota-node --config-path /opt/iota/config/fullnode.yaml "$@"
