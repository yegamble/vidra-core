-- Refuse a downgrade while protected input sessions remain. Reclassifying
-- their bytes as a regular upload would lose the processing policy.
ALTER TABLE upload_sessions DROP CONSTRAINT upload_sessions_processed_filename_check,
    DROP CONSTRAINT upload_sessions_purpose_check,
    ADD CONSTRAINT upload_sessions_purpose_check CHECK (purpose IN ('upload', 'replace'));
