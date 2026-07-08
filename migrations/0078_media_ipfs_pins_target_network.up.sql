-- 0078: two-phase cross-network pin transitions (fix_plan P19.P2,
-- .ralph/specs/ipfs-media-private.md §3/§8). A privacy flip transitions the SAME
-- ledger row between swarms — object_key stays the PK, so an object can never be
-- pinned on both networks at once. The transition is two-phase and rides the
-- existing CAS state machine: the row is first set state='unpinning' on its CURRENT
-- network (removing the bytes from that node) with target_network recording where it
-- goes NEXT; once the unpin completes, MarkIPFSPinUnpinned re-arms the SAME row to
-- network=target_network, state='pending', cid='' so the worker adds+pins it on the
-- new swarm. Going through 'unpinning' (even when cid='' at flip time) is what makes
-- an in-flight Add safe: MarkIPFSPinned's 'unpinning' guard keeps the row unpinning,
-- so a public-node CID is never recorded onto a row bound for the private network —
-- the failure mode is always "content unreachable", never "private content public".
-- NULL = no transition in flight (the steady state for every row). Existing rows are
-- all steady-state, so the column is nullable with no default backfill needed.
ALTER TABLE media_ipfs_pins
    ADD COLUMN target_network TEXT
        CHECK (target_network IS NULL OR target_network IN ('public', 'private'));
