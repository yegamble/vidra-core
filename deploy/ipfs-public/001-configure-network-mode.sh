#!/bin/sh
# Configure the compose Kubo node either as a local-only test node (default) or
# as a real provider on the public IPFS network (IPFS_PUBLIC_NETWORK=true).
#
# This hook runs after `ipfs init` and before the daemon starts. Keeping the
# public transition here makes it work for both fresh and persisted repos: a
# node can be deliberately moved into live mode without hand-editing config,
# while a default compose start cannot silently publish content to the DHT.
set -eu

log() { echo "[ipfs-network-init] $*"; }

case "${IPFS_PUBLIC_NETWORK:-false}" in
	1 | true | TRUE | yes | YES)
		# Rejoin the default bootstrap set if this volume previously ran in local
		# mode, enable Kubo's supported provider profile, and make a NAT'd node
		# reachable through relay reservations / hole punching.
		# Restore Kubo 0.41's `server` public-address filters after the local
		# path's deny-all swarm gate on a persisted repo. Reapplying the profile
		# is insufficient because profiles append filters instead of removing
		# the two deny-all entries.
		ipfs config --json Swarm.AddrFilters \
			'["/ip4/10.0.0.0/ipcidr/8",
			  "/ip4/100.64.0.0/ipcidr/10",
			  "/ip4/127.0.0.0/ipcidr/8",
			  "/ip4/169.254.0.0/ipcidr/16",
			  "/ip4/172.16.0.0/ipcidr/12",
			  "/ip4/192.0.0.0/ipcidr/24",
			  "/ip4/192.0.2.0/ipcidr/24",
			  "/ip4/192.168.0.0/ipcidr/16",
			  "/ip4/198.18.0.0/ipcidr/15",
			  "/ip4/198.51.100.0/ipcidr/24",
			  "/ip4/203.0.113.0/ipcidr/24",
			  "/ip4/240.0.0.0/ipcidr/4",
			  "/ip6/::/ipcidr/3",
			  "/ip6/::1/ipcidr/128",
			  "/ip6/100::/ipcidr/64",
			  "/ip6/2001:2::/ipcidr/48",
			  "/ip6/2001:db8::/ipcidr/32",
			  "/ip6/fc00::/ipcidr/7",
			  "/ip6/fe80::/ipcidr/10"]'
		# Kubo 0.41 represents its default AutoConf bootstrap set with the
		# special `auto` entry (`bootstrap add --default` was removed).
		ipfs config --json AutoConf.Enabled true
		ipfs config --json DNS.Resolvers '{".":"auto"}'
		ipfs config --json Routing.DelegatedRouters '["auto"]'
		ipfs config --json Ipns.DelegatedPublishers '["auto"]'
		ipfs config --json AutoTLS.Enabled true
		ipfs bootstrap add auto >/dev/null
		ipfs config Routing.Type auto
		ipfs config profile apply announce-on >/dev/null
		ipfs config --json Gateway.NoFetch false
		ipfs config --json Swarm.RelayClient.Enabled true
		ipfs config --json Swarm.EnableHolePunching true
		ipfs config --json Swarm.RelayService.Enabled true
		ipfs config --json AutoNAT.ServiceMode '"enabled"'
		log "LIVE PUBLIC mode enabled (DHT providing + relay/hole-punch reachability)"
		log "WARNING: every CID added here is a permanent public-disclosure decision"
		;;
	0 | false | FALSE | no | NO | "")
		# A local integration node must be genuinely local: no bootstrap peers,
		# no content router, no provider queue, and no relay/NAT traversal. The
		# local HTTP gateway can still serve blocks already present in this repo.
		ipfs config --json AutoConf.Enabled false
		ipfs config --json DNS.Resolvers '{}'
		ipfs config --json Routing.DelegatedRouters '[]'
		ipfs config --json Ipns.DelegatedPublishers '[]'
		ipfs config --json AutoTLS.Enabled false
		ipfs bootstrap rm --all >/dev/null 2>&1 || true
		ipfs config Routing.Type none
		ipfs config profile apply announce-off >/dev/null
		ipfs config --json Gateway.NoFetch true
		ipfs config --json Swarm.RelayClient.Enabled false
		ipfs config --json Swarm.EnableHolePunching false
		ipfs config --json Swarm.RelayService.Enabled false
		ipfs config --json AutoNAT.ServiceMode '"disabled"'
		# Bootstrap removal is not sufficient on a repo that previously ran live:
		# libp2p can redial peers retained in its peerstore. Deny every IP multiaddr
		# so neither remembered nor inbound public peers can cross local-only mode.
		ipfs config --json Swarm.AddrFilters \
			'["/ip4/0.0.0.0/ipcidr/0","/ip6/::/ipcidr/0"]'
		log "local-only mode enabled (all swarm addresses + bootstrap/routing/providing/relay disabled)"
		;;
	*)
		log "ERROR: IPFS_PUBLIC_NETWORK must be true or false"
		exit 64
		;;
esac
