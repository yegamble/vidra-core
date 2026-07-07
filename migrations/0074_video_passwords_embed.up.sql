-- 0074: video passwords + embed privacy (CORE-17, backport wave W1.C2;
-- .ralph/specs/backport-w1-watch-player.md).
--
-- Part A — password-protected videos. A new privacy tier 'password' plus a table
-- of bcrypt password hashes (write-only: a plaintext or hash is NEVER returned by
-- the API). A viewer unlocks a password video via POST /videos/{id}/unlock, which
-- verifies the plaintext against these hashes and mints a short-lived,
-- video-scoped HMAC playback token; the media/detail read endpoints accept that
-- token (Bearer or ?pt=). A video may only be privacy='password' while it has >=1
-- password (enforced in the service; the widened CHECK just allows the value).
-- Password videos are excluded from public discovery exactly like 'unlisted' —
-- the feed/search/channel/trending queries already filter privacy='public'.
--
-- Part B — embed privacy. Two columns on videos: a status tier and, for the
-- 'whitelist' tier, an allow-list of hostnames. Enforcement is at the embed page
-- (referrer / ancestor-origin check in vidra-user); the server only stores the
-- policy and serves it back. Server-side Referer enforcement is a non-goal
-- (spoofable, breaks proxies).

-- Widen the privacy CHECK to include 'password' (same drop/add pattern as the
-- state-check widening in 0045/0048; the inline column check is auto-named
-- videos_privacy_check).
ALTER TABLE videos DROP CONSTRAINT videos_privacy_check;
ALTER TABLE videos ADD CONSTRAINT videos_privacy_check
    CHECK (privacy IN ('public', 'unlisted', 'private', 'password'));

-- Embed-privacy policy columns (one row per video, no fan-out). Defaults keep
-- every existing video embeddable ('enabled') with no domain restriction.
ALTER TABLE videos
    ADD COLUMN embed_privacy text NOT NULL DEFAULT 'enabled'
        CHECK (embed_privacy IN ('enabled', 'disabled', 'whitelist')),
    ADD COLUMN embed_allowed_domains text[] NOT NULL DEFAULT '{}';

-- Bcrypt password hashes for password-protected videos. The plaintext is
-- write-only; only the id + created_at are ever exposed (owner listing). ON
-- DELETE CASCADE ties the rows to the owning video's lifecycle.
CREATE TABLE video_passwords (
    id            uuid        PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id      uuid        NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    password_hash text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX video_passwords_video_id_idx ON video_passwords (video_id);
