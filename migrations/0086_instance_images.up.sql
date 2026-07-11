-- 0086: instance-scoped branding images (config-parity W1).
--
-- The instance's own avatar/banner plus the four typed logo slots PeerTube
-- exposes (favicon, header-wide, header-square, opengraph). A thin singleton
-- side table mirroring user_images/channel_images (migration 0040) with the
-- kind as the whole key — the instance is a singleton owner, so no owner id
-- column is needed and the shared profileimage store/replace/delete pipeline
-- is reused unchanged. storage_key is the backend object key
-- (avatars/instance/…, banners/instance/…, logos/instance/<kind>…), opaque to
-- the database. One row per kind; re-upload replaces it.
CREATE TABLE instance_images (
    kind         TEXT        PRIMARY KEY CHECK (kind IN ('avatar', 'banner', 'favicon', 'header-wide', 'header-square', 'opengraph')),
    storage_key  TEXT        NOT NULL,
    content_type TEXT        NOT NULL DEFAULT '',
    size_bytes   BIGINT      NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
