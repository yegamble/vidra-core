-- Returned roots remain recoverable even if the source claim loses its lease
-- before it can commit. Only roots produced by managed copies enter this queue.
CREATE TABLE ipfs_copy_cleanup (
    claim_token UUID NOT NULL,
    cid TEXT NOT NULL CHECK (cid <> ''),
    object_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (claim_token, cid)
);
ALTER TABLE ipfs_capacity
    ADD COLUMN cleanup_pending BIGINT NOT NULL DEFAULT 0 CHECK (cleanup_pending >= 0),
    ADD COLUMN maintenance_token UUID,
    ADD COLUMN maintenance_until TIMESTAMPTZ,
    ADD COLUMN maintenance_host_sequence BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN maintenance_config_revision BIGINT NOT NULL DEFAULT 0;
INSERT INTO ipfs_capacity DEFAULT VALUES ON CONFLICT DO NOTHING;
