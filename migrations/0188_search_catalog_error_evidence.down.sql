ALTER TABLE search_catalog_state
    DROP CONSTRAINT search_catalog_error_evidence_pair,
    DROP CONSTRAINT search_catalog_error_evidence_nonzero,
    DROP COLUMN last_error_id;
