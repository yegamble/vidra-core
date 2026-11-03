ALTER TABLE search_catalog_state
    DROP COLUMN last_error_code,
    DROP COLUMN force_full_after_generation;
ALTER TABLE search_catalog_epochs
    DROP CONSTRAINT search_catalog_source_predecessor_check,
    DROP CONSTRAINT search_catalog_source_predecessor_fkey,
    DROP COLUMN source_predecessor;
