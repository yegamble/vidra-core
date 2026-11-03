-- Source capture writes bounded pages inside one RR transaction, then inserts
-- the final manifest. Deferred page ownership permits that streaming order.
ALTER TABLE search_catalog_state
    ADD CONSTRAINT search_catalog_state_scope_epoch_key UNIQUE (scope, epoch);

CREATE TABLE search_catalog_snapshots (
    scope TEXT NOT NULL,
    epoch UUID NOT NULL,
    generation BIGINT NOT NULL CHECK (generation > 0),
    pending BOOLEAN NOT NULL DEFAULT true,
    page_count INTEGER NOT NULL CHECK (page_count BETWEEN 0 AND 1024),
    document_count BIGINT NOT NULL CHECK (document_count BETWEEN 0 AND 100000),
    canonical_bytes BIGINT NOT NULL CHECK (canonical_bytes BETWEEN 0 AND 536870912),
    membership_sha256 TEXT NOT NULL CHECK (membership_sha256 ~ '^[0-9a-f]{64}$'),
    pages_sha256 TEXT NOT NULL CHECK (pages_sha256 ~ '^[0-9a-f]{64}$'),
    end_event_id UUID NOT NULL CHECK (end_event_id <> '00000000-0000-0000-0000-000000000000'),
    end_request_body BYTEA NOT NULL CHECK (octet_length(end_request_body) BETWEEN 1 AND 1048576),
    captured_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, epoch, generation),
    FOREIGN KEY (scope, epoch) REFERENCES search_catalog_state(scope, epoch) ON DELETE CASCADE,
    CHECK (page_count <= document_count),
    CHECK ((page_count = 0) = (document_count = 0)),
    CHECK ((document_count = 0) = (canonical_bytes = 0)),
    CHECK (page_count > 0 OR
        (membership_sha256 = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'
        AND pages_sha256 = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'))
);

CREATE UNIQUE INDEX search_catalog_one_pending ON search_catalog_snapshots(scope) WHERE pending;

CREATE TABLE search_catalog_pages (
    scope TEXT NOT NULL,
    epoch UUID NOT NULL,
    generation BIGINT NOT NULL,
    page_index INTEGER NOT NULL CHECK (page_index BETWEEN 0 AND 1023),
    event_id UUID NOT NULL CHECK (event_id <> '00000000-0000-0000-0000-000000000000'),
    request_body BYTEA NOT NULL CHECK (octet_length(request_body) BETWEEN 1 AND 1048576),
    document_count INTEGER NOT NULL CHECK (document_count BETWEEN 1 AND 100000),
    canonical_bytes BIGINT NOT NULL CHECK (canonical_bytes BETWEEN 1 AND 1048576),
    content_sha256 TEXT NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (scope, epoch, generation, page_index),
    FOREIGN KEY (scope, epoch, generation)
        REFERENCES search_catalog_snapshots(scope, epoch, generation)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED
);

-- Retention may remove superseded payloads, but not the current verified proof.
ALTER TABLE search_catalog_state
    ADD CONSTRAINT search_catalog_verified_manifest_fkey
        FOREIGN KEY (scope, epoch, verified_generation)
        REFERENCES search_catalog_snapshots(scope, epoch, generation);
