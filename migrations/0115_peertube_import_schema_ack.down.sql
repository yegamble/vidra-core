-- Roll back 0115. Dropping the acknowledgement column loses the record of who
-- signed off on which unverified schema; that history only exists here, so this
-- direction is a real loss of audit and not merely a shape change.
ALTER TABLE peertube_import_runs
    DROP COLUMN IF EXISTS error_code,
    DROP COLUMN IF EXISTS acknowledged_schema_version;
