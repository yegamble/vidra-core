#!/bin/sh
# IPFS Kubo node configuration for public network participation.
# Mounted into /container-init.d/ — Kubo runs these scripts after `ipfs init`
# but before the daemon starts. This enables NAT traversal so pinned content
# is discoverable and retrievable by public IPFS gateways.
set -e

echo "[ipfs-init] Configuring IPFS node for public network participation..."

# Enable AutoRelay client — the node will find and use public relay nodes
# to make itself reachable even when behind NAT/firewall.
ipfs config --json Swarm.RelayClient.Enabled true

# Enable hole punching — allows direct peer-to-peer connections through NAT
# by coordinating with the relay to establish a direct link.
ipfs config --json Swarm.EnableHolePunching true

# Enable RelayService so this node can also help OTHER nodes behind NAT
# (good network citizenship, optional but recommended for server profile).
ipfs config --json Swarm.RelayService.Enabled true

# Ensure the node is not in server mode for relay purposes — server profile
# disables AutoNAT dialing by default which we need for relay discovery.
# Re-enable AutoNAT so the node can determine its own reachability.
ipfs config --json AutoNAT.ServiceMode '"enabled"'

echo "[ipfs-init] IPFS node configured for public network access."
