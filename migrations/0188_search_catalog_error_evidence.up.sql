-- A repeated error code is a new observation. An older sender must not clear
-- it merely because epoch, pending FULL and the text code still match.
ALTER TABLE search_catalog_state ADD COLUMN last_error_id UUID;
UPDATE search_catalog_state SET last_error_id=uuid_generate_v4()
WHERE last_error_code IS NOT NULL;
ALTER TABLE search_catalog_state
    ADD CONSTRAINT search_catalog_error_evidence_pair
        CHECK ((last_error_code IS NULL) = (last_error_id IS NULL)),
    ADD CONSTRAINT search_catalog_error_evidence_nonzero
        CHECK (last_error_id IS NULL OR last_error_id <> '00000000-0000-0000-0000-000000000000');

COMMENT ON COLUMN search_catalog_state.last_error_id IS 'Current error evidence only: rotate on each error and compare expected nullable ID in ACK/error CAS; no receipt history.';
