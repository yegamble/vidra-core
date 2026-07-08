-- 0077: private IPFS mirroring — route each pin ledger row to exactly one swarm
-- (fix_plan P19.P1, .ralph/specs/ipfs-media-private.md §3). The public mirror
-- (P19.1–P19.10) only ever enqueued ALREADY-PUBLIC media, so every existing row is
-- public: the column defaults to 'public' and the CHECK constrains it to the two
-- networks. The worker reads this column and routes the row to the matching node
-- client — a 'private' row is pinned ONLY through the dedicated private-swarm kubo,
-- NEVER the public client (the phase's cardinal fail-closed invariant). An object
-- lives on exactly one network at a time (object_key stays the PK); a privacy flip
-- (P19.P2) transitions the SAME row across networks via the ledger state machine.
ALTER TABLE media_ipfs_pins
    ADD COLUMN network TEXT NOT NULL DEFAULT 'public'
        CHECK (network IN ('public', 'private'));

-- Per-network admin counts + the network-scoped worker claim/reconcile scans slice
-- by (network, state, media_class), mirroring the public-only state_class index.
CREATE INDEX media_ipfs_pins_network_state_idx
    ON media_ipfs_pins (network, state, media_class);
