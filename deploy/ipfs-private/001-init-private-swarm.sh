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
if [ ! -f "$KEY_SHARED" ]; then
	log "generating a DEV swarm.key (NOT for production) at $KEY_SHARED"
	mkdir -p "$(dirname "$KEY_SHARED")"
	{
		printf '/key/swarm/psk/1.0.0/\n'
		printf '/base16/\n'
		head -c 32 /dev/urandom | od -A none -t x1 | tr -d ' \n'
		printf '\n'
	} >"$KEY_SHARED"
	chmod 600 "$KEY_SHARED"
fi
cp "$KEY_SHARED" "$KEY_DST"
chmod 600 "$KEY_DST"
log "swarm.key installed at $KEY_DST"

# 2. Network isolation (belt-and-suspenders on top of LIBP2P_FORCE_PNET=1): no public
#    bootstrap peers, routing OFF (explicit peering only), reprovide OFF (keep even the
#    private DHT quiet), and the gateway serves ONLY local repo content (NoFetch) and
#    is never host-published — private CIDs are replication, not distribution (§5).
ipfs bootstrap rm --all >/dev/null 2>&1 || true
ipfs config Routing.Type none
ipfs config Reprovider.Interval 0
ipfs config --json Gateway.NoFetch true
# Reject any accidental public announce: bind the gateway to the container only and
# keep the API on all interfaces of the compose network (RPC is the app's path in).
ipfs config Addresses.Gateway /ip4/127.0.0.1/tcp/8080
log "isolation config applied (bootstrap cleared, Routing=none, Reprovider=0, Gateway.NoFetch=true)"

# 3. Publish this node's peer id to the shared volume so a peer node can dial it, and —
#    when PRIVATE_PEER_HOST is set (the optional second node) — configure explicit
#    Peering to the other node (Routing=none means peers must be pinned explicitly).
if [ -n "${PRIVATE_SELF_NAME:-}" ]; then
	SELF_ID="$(ipfs config Identity.PeerID)"
	echo "$SELF_ID" >"/private-key/${PRIVATE_SELF_NAME}.peerid"
	log "published peer id for ${PRIVATE_SELF_NAME}"
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
