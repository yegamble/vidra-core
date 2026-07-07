-- 0075: per-user player settings (PLAY-07, backport wave W1.C3;
-- .ralph/specs/backport-w1-watch-player.md).
--
-- One row per user holding their default playback preferences: autoplay of the
-- next video, playback speed, default quality, and whether captions / theater
-- mode start on. A user who never saved has NO row — the API serves the built-in
-- defaults (this table's column DEFAULTs), and the first PUT upserts the row.
-- The PUT is a merge (a partial body keeps the stored value for omitted fields),
-- applied at the service layer via read-modify-write.
--
-- Scope is PER-USER ONLY — the archived openapi_player_settings.yaml scoped
-- settings per-video/per-channel; PLAY-07 (and this wave) is per-user defaults,
-- so per-video/per-channel overrides are deliberately not ported.
--
-- default_speed is numeric(4,2) so the exact ladder rungs (0.25 … 1 … 1.75 … 4)
-- store without float drift; the service validates the value is one of the shared
-- PLAYBACK_RATES before writing, and the API reads/writes it as a JSON number.
-- default_quality is 'auto' or a rendition height like '720p' (service-validated,
-- same ^[0-9]{2,4}p$ shape as the HLS rendition path segment).
CREATE TABLE user_player_settings (
    user_id          uuid         PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    autoplay_next    boolean      NOT NULL DEFAULT true,
    default_speed    numeric(4,2) NOT NULL DEFAULT 1,
    default_quality  text         NOT NULL DEFAULT 'auto',
    captions_default boolean      NOT NULL DEFAULT false,
    theater_default  boolean      NOT NULL DEFAULT false,
    updated_at       timestamptz  NOT NULL DEFAULT now()
);
