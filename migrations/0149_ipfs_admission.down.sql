DROP TABLE ipfs_capacity;
DROP INDEX media_ipfs_pins_claim_idx;
DROP INDEX media_ipfs_pins_public_cid_idx;
ALTER TABLE media_ipfs_pins
    DROP COLUMN capacity_reason, DROP COLUMN demand_at, DROP COLUMN policy_reason,
    DROP COLUMN admitted_host_sequence, DROP COLUMN admitted_config_revision, DROP COLUMN committed_generation,
    DROP COLUMN source_generation, DROP COLUMN copied_bytes,
    DROP COLUMN reservation_bytes, DROP COLUMN lease_until, DROP COLUMN claim_token;
