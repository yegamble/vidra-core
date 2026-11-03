-- Source authority survives payload cleanup and remains dormant until explicit
-- bootstrap. A receiver predecessor permits the exact epoch-CAS retry after a
-- lost response; it need not belong to this source's own epoch history.
CREATE TABLE search_catalog_epochs (
    scope TEXT NOT NULL CHECK (scope = 'local'),
    epoch UUID NOT NULL CHECK (epoch <> '00000000-0000-0000-0000-000000000000'),
    receiver_predecessor UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, epoch),
    CHECK (receiver_predecessor IS NULL OR
        (receiver_predecessor <> epoch AND receiver_predecessor <> '00000000-0000-0000-0000-000000000000'))
);

CREATE TABLE search_catalog_state (
    scope TEXT PRIMARY KEY CHECK (scope = 'local'),
    epoch UUID NOT NULL,
    allocated_generation BIGINT NOT NULL DEFAULT 0 CHECK (allocated_generation >= 0),
    verified_generation BIGINT CHECK (verified_generation > 0 AND verified_generation <= allocated_generation),
    verified_at TIMESTAMPTZ,
    force_full BOOLEAN NOT NULL DEFAULT true,
    FOREIGN KEY (scope, epoch) REFERENCES search_catalog_epochs(scope, epoch),
    CHECK ((verified_generation IS NULL) = (verified_at IS NULL))
);

-- These immutable identities are hints, never authoritative metadata. Capture
-- acknowledges its exact visible UUID set, not a sequence/time high-water mark.
CREATE TABLE search_catalog_dirty (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4()
        CHECK (id <> '00000000-0000-0000-0000-000000000000'),
    scope TEXT NOT NULL CHECK (scope = 'local'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE search_catalog_epochs IS 'Permanent source epoch/CAS history; never pruned with dirty hints or frozen payloads.';
COMMENT ON COLUMN search_catalog_state.verified_generation IS 'Requires an exact retained FULL manifest, not delivery acceptance or receiver staging.';
