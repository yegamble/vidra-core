ALTER TABLE ipfs_capacity DROP COLUMN maintenance_config_revision,
    DROP COLUMN maintenance_host_sequence, DROP COLUMN maintenance_until,
    DROP COLUMN maintenance_token, DROP COLUMN cleanup_pending;
DROP TABLE ipfs_copy_cleanup;
