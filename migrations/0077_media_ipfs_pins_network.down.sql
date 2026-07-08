-- Roll back 0077: drop the per-network index and the network routing column.
DROP INDEX IF EXISTS media_ipfs_pins_network_state_idx;
ALTER TABLE media_ipfs_pins DROP COLUMN IF EXISTS network;
