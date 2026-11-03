-- Preserve the original source CAS predecessor for an exact bootstrap retry,
-- even after capture/delivery advances within the accepted new epoch.
ALTER TABLE search_catalog_epochs
    ADD COLUMN source_predecessor UUID,
    ADD CONSTRAINT search_catalog_source_predecessor_fkey
        FOREIGN KEY (scope, source_predecessor) REFERENCES search_catalog_epochs(scope, epoch),
    ADD CONSTRAINT search_catalog_source_predecessor_check
        CHECK (source_predecessor IS NULL OR source_predecessor <> epoch);

-- A fresh-full request arriving behind frozen generation G requires a verified
-- generation > G. A boolean alone can be cleared incorrectly by that old G.
ALTER TABLE search_catalog_state
    ADD COLUMN force_full_after_generation BIGINT NOT NULL DEFAULT 0
        CHECK (force_full_after_generation >= 0 AND force_full_after_generation <= allocated_generation),
    ADD COLUMN last_error_code TEXT CHECK (last_error_code IN (
        'quota_exceeded', 'scalar_limit_exceeded', 'capture_failed',
        'delivery_failed', 'verification_failed', 'epoch_conflict'));

COMMENT ON COLUMN search_catalog_state.last_error_code IS 'Durable sanitized blocker; no raw upstream errors or document data.';
