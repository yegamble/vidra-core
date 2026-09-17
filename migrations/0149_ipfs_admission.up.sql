-- Reservations survive worker failure. An expired claim is not free space:
-- recovery must prove the node stopped the old request before releasing it.
ALTER TABLE media_ipfs_pins
    ADD COLUMN claim_token UUID,
    ADD COLUMN lease_until TIMESTAMPTZ,
    ADD COLUMN reservation_bytes BIGINT NOT NULL DEFAULT 0 CHECK (reservation_bytes >= 0),
    ADD COLUMN copied_bytes BIGINT NOT NULL DEFAULT 0 CHECK (copied_bytes >= 0),
    ADD COLUMN source_generation TEXT NOT NULL DEFAULT '',
    ADD COLUMN committed_generation TEXT NOT NULL DEFAULT '',
    ADD COLUMN admitted_host_sequence BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN admitted_config_revision BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN policy_reason TEXT NOT NULL DEFAULT 'legacy'
        CHECK (policy_reason IN ('legacy', 'new', 'demand', 'backfill')),
    ADD COLUMN demand_at TIMESTAMPTZ,
    ADD COLUMN capacity_reason TEXT NOT NULL DEFAULT '';

CREATE INDEX media_ipfs_pins_public_cid_idx ON media_ipfs_pins (cid)
    WHERE network = 'public' AND state = 'pinned';
CREATE INDEX media_ipfs_pins_claim_idx ON media_ipfs_pins (lease_until)
    WHERE claim_token IS NOT NULL;

-- A single UPDATE counter serializes admission even under READ COMMITTED:
-- summing reservation rows after an advisory lock would use a stale snapshot.
CREATE TABLE ipfs_capacity (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    reserved_bytes BIGINT NOT NULL DEFAULT 0 CHECK (reserved_bytes >= 0),
    active_claims INTEGER NOT NULL DEFAULT 0 CHECK (active_claims >= 0),
    measure_after TIMESTAMPTZ NOT NULL DEFAULT 'epoch'
);
