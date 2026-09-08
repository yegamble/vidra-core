DROP TRIGGER IF EXISTS federation_deliveries_run_identity ON federation_deliveries;
DROP FUNCTION IF EXISTS federation_delivery_run_identity();
ALTER TABLE federation_deliveries
    DROP COLUMN IF EXISTS correlation_id,
    DROP COLUMN IF EXISTS request_id;
