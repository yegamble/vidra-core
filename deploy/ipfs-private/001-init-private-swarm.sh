#!/bin/sh
# Private-swarm bootstrap for the compose `kubo-private` node(s) (fix_plan P19.P3,
# .ralph/specs/ipfs-media-private.md §9). Runs via the ipfs/kubo image's
# /container-init.d hook AFTER `ipfs init` but BEFORE `ipfs daemon`, so the swarm.key
# and network-isolation config are in place before LIBP2P_FORCE_PNET=1 is enforced.
#
# ⚠️ DEV-ONLY key generation. In PRODUCTION the swarm.key is operator-managed: you
# generate it ONCE, distribute the SAME file to every node, and mount it read-only
# (see README / .env.example). Possession of the key == full private-network
# membership — there is no per-node revocation and rotation means a new key + a
# coordinated restart of every node. NEVER commit a real swarm.key.
set -eu

IPFS_PATH="${IPFS_PATH:-/data/ipfs}"
KEY_DST="$IPFS_PATH/swarm.key"
# Shared volume so a multi-node dev swarm (the optional cluster leg) uses ONE key.
KEY_SHARED="/private-key/swarm.key"

log() { echo "[private-swarm-init] $*"; }

# 1. swarm.key: generate a dev key into the shared volume on first boot, else reuse
#    the operator-mounted / already-generated one. The PSK format is three lines:
#    the codec header, the base16 line, then 32 random bytes as 64 hex chars.
# The root entrypoint prelude (000-private-key-perms.sh) has already chowned the shared
# volume so the ipfs user can write here on the dev auto-gen path. In the operator path
# the key is mounted read-only and already exists, so generation is skipped entirely.
KEY_DIR="$(dirname "$KEY_SHARED")"
if [ ! -f "$KEY_SHARED" ]; then
	if [ -w "$KEY_DIR" ]; then
		log "generating a DEV swarm.key (NOT for production) at $KEY_SHARED"
		{
			printf '/key/swarm/psk/1.0.0/\n'
			printf '/base16/\n'
			head -c 32 /dev/urandom | od -A none -t x1 | tr -d ' \n'
			printf '\n'
		} >"$KEY_SHARED"
		chmod 600 "$KEY_SHARED"
	else
		# No shared key AND the shared dir is not writable: this hook can't provide one.
		# The daemon then fail-closes (LIBP2P_FORCE_PNET=1 with no swarm.key = refuse to
		# boot) — "private content unreachable", never "private content public". In
		# production mount your operator swarm.key at this path read-only so this is skipped.
		log "WARN: no swarm.key at $KEY_SHARED and $KEY_DIR is not writable — cannot auto-generate"
	fi
fi
if [ -f "$KEY_SHARED" ]; then
	cp "$KEY_SHARED" "$KEY_DST"
	chmod 600 "$KEY_DST"
	log "swarm.key installed at $KEY_DST"
fi

# 2. Network isolation (belt-and-suspenders on top of LIBP2P_FORCE_PNET=1): no public
#    bootstrap peers, routing OFF (explicit peering only), providing OFF (keep even the
#    private DHT quiet), and the gateway serves ONLY local repo content (NoFetch) and
#    is never host-published — private CIDs are replication, not distribution (§5).
ipfs bootstrap rm --all >/dev/null 2>&1 || true
ipfs config Routing.Type none
# Kubo 0.41 refuses to start a private-network repo while the public-mainnet
# AutoConf endpoint is enabled. More importantly, a private swarm must never
# consult mainnet configuration in the first place.
ipfs config --json AutoConf.Enabled false
# Remove the `auto` consumers as well. Leaving these behind is safe at runtime
# once AutoConf is disabled, but Kubo logs them as configuration errors and they
# would become mainnet-capable again if this single flag were ever regressed.
ipfs config --json DNS.Resolvers '{}'
ipfs config --json Routing.DelegatedRouters '[]'
ipfs config --json Ipns.DelegatedPublishers '[]'
# AutoTLS and the shared TCP/WebSocket listener are explicitly incompatible
# with PNet in Kubo 0.41. Disable both instead of accepting error-level logs.
ipfs config --json AutoTLS.Enabled false
ipfs config --json Swarm.Transports.Network.Websocket false
# Kubo 0.38+ replaced Reprovider.Interval with the Provide section. The built-in
# profile is migration-aware and sets both Provide.Enabled=false and a zero DHT
# interval on current repos (and the legacy equivalent on older repos).
ipfs config profile apply announce-off >/dev/null
ipfs config --json Gateway.NoFetch true
ipfs config --json Swarm.RelayClient.Enabled false
ipfs config --json Swarm.EnableHolePunching false
ipfs config --json Swarm.RelayService.Enabled false
ipfs config --json AutoNAT.ServiceMode '"disabled"'
# The `server` IPFS_PROFILE (init above) installs Swarm.AddrFilters that BLOCK dialing
# every private IP range (10/8, 172.16/12, 192.168/16, …) — it assumes a public node.
# A private swarm lives ENTIRELY on a private network (the compose bridge is 172.x, a
# prod deployment a VPC), so those filters make the keyed peers unable to dial each other
# ("gater disallows connection to peer") and replication never happens. Clear them:
# isolation here comes from the swarm.key / LIBP2P_FORCE_PNET, NOT from IP filtering, so
# allowing private-address dialing is both correct and required for the swarm to function.
ipfs config --json Swarm.AddrFilters '[]'
# Kubo 0.41's server profile also suppresses RFC1918 announce addresses. Private
# peers intentionally live on those addresses, so allow them inside the PNet.
ipfs config --json Addresses.NoAnnounce '[]'
# Reject any accidental public announce: bind the gateway to the container only and
# keep the API on all interfaces of the compose network (RPC is the app's path in).
ipfs config Addresses.Gateway /ip4/127.0.0.1/tcp/8080
log "isolation config applied (AutoConf/AutoTLS/bootstrap/routing/providing/relay disabled, Gateway.NoFetch=true, private addresses allowed for intra-swarm dialing)"

# 3. Publish this node's peer id to the shared volume so a peer node can dial it, and —
#    when PRIVATE_PEER_HOST is set (the optional second node) — configure explicit
#    Peering to the other node (Routing=none means peers must be pinned explicitly).
if [ -n "${PRIVATE_SELF_NAME:-}" ]; then
	if [ -w /private-key ]; then
		SELF_ID="$(ipfs config Identity.PeerID)"
		echo "$SELF_ID" >"/private-key/${PRIVATE_SELF_NAME}.peerid"
		log "published peer id for ${PRIVATE_SELF_NAME}"
	else
		# Read-only shared dir (operator single-node prod): peer-id exchange is a
		# multi-node dev convenience only, so skip it rather than abort init.
		log "WARN: /private-key not writable — skipping peer-id publish for ${PRIVATE_SELF_NAME}"
	fi
fi
if [ -n "${PRIVATE_PEER_HOST:-}" ]; then
	PEER_FILE="/private-key/${PRIVATE_PEER_HOST}.peerid"
	if [ -f "$PEER_FILE" ]; then
		PEER_ID="$(cat "$PEER_FILE")"
		ipfs config --json Peering.Peers \
			"[{\"ID\":\"${PEER_ID}\",\"Addrs\":[\"/dns4/${PRIVATE_PEER_HOST}/tcp/4001\"]}]"
		log "explicit peering configured to ${PRIVATE_PEER_HOST} (${PEER_ID})"
	else
		log "WARN: peer id file $PEER_FILE not found; skipping explicit peering"
	fi
fi
