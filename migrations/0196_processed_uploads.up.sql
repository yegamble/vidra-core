-- The staging filename is deliberately outside the video extension allowlist.
-- A previous worker encountering this session cannot attach the creator's raw bytes.
ALTER TABLE upload_sessions DROP CONSTRAINT upload_sessions_purpose_check,
    ADD CONSTRAINT upload_sessions_purpose_check
        CHECK (purpose IN ('upload', 'replace', 'processed')),
    ADD CONSTRAINT upload_sessions_processed_filename_check
        CHECK (purpose <> 'processed' OR right(filename, 16) = '.vidra-unscanned');
