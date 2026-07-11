-- 0085: instance documents (config-parity W1, core config surface foundations).
--
-- Long-form, admin-authored documents that live OUTSIDE the instance_settings
-- registry (its 10k string cap, PATCH-scalar semantics, and diff-scale
-- auditing don't fit document-scale bodies): the custom homepage markdown and
-- the custom CSS/JS injected into every page. One row per document; a PUT with
-- an empty body deletes the row (public delivery 404s while unset). sha256 is
-- the lowercase hex content hash, surfaced in GET /instance so clients can
-- hash-bust (<link href="/instance/custom.css?v={hash}">).
CREATE TABLE instance_documents (
    name       TEXT        PRIMARY KEY CHECK (name IN ('homepage', 'custom_css', 'custom_js')),
    body       TEXT        NOT NULL,
    sha256     TEXT        NOT NULL,
    -- The admin who last wrote this document. Nullable + ON DELETE SET NULL so
    -- the document survives that admin's account deletion (audit-integrity
    -- parity with instance_settings.updated_by).
    updated_by UUID        REFERENCES users (id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
