-- Down: drop the cross-network transition marker. Any in-flight transition is lost
-- (rows revert to single-network steady state); the eligibility sweep + reconcile
-- re-derive routing from committed visibility facts, so no pin is stranded.
ALTER TABLE media_ipfs_pins DROP COLUMN IF EXISTS target_network;
