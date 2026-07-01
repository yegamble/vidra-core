-- 0025: optional per-video taxonomy metadata. category and license reference the
-- GET /api/v1/videos/config maps (PeerTube-compatible numeric ids, as text);
-- language is an ISO 639-1 code. All nullable — NULL means "unset". Validation of
-- the values against the config maps happens in the application layer (which owns
-- the canonical lists), so no DB CHECK duplicates them here.
ALTER TABLE videos
    ADD COLUMN category TEXT,
    ADD COLUMN language TEXT,
    ADD COLUMN license  TEXT;
