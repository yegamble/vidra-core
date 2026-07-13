-- name: GetUserPlayerSettings :one
-- The caller's stored player settings. A miss (no row) means the user never
-- saved; the service serves effective defaults in that case. The nullable
-- video-card preview value remains an inherited admin default until the user
-- explicitly chooses. default_speed is
-- read through a COALESCE+cast so it maps to a plain float64 (never pgtype.Numeric
-- and never a pointer — the column is NOT NULL, COALESCE just proves it to sqlc).
SELECT user_id,
       autoplay_next,
       COALESCE(default_speed, 1)::float8 AS default_speed,
       default_quality,
       captions_default,
       theater_default,
       video_card_previews_enabled,
       updated_at
FROM user_player_settings
WHERE user_id = $1;

-- name: UpsertUserPlayerSettings :exec
-- Insert or replace the caller's stored player settings. The merge
-- (keep-stored-value for omitted fields) is done at the service layer via
-- read-modify-write. video_card_previews_enabled stays NULL while inherited;
-- all other fields are always written. default_speed is passed as a float8 and
-- assignment-cast into the numeric(4,2) column.
INSERT INTO user_player_settings (
    user_id, autoplay_next, default_speed, default_quality, captions_default, theater_default,
    video_card_previews_enabled
) VALUES (
    sqlc.arg('user_id'),
    sqlc.arg('autoplay_next'),
    sqlc.arg('default_speed')::float8,
    sqlc.arg('default_quality'),
    sqlc.arg('captions_default'),
    sqlc.arg('theater_default'),
    sqlc.narg('video_card_previews_enabled')
)
ON CONFLICT (user_id) DO UPDATE SET
    autoplay_next    = EXCLUDED.autoplay_next,
    default_speed    = EXCLUDED.default_speed,
    default_quality  = EXCLUDED.default_quality,
    captions_default = EXCLUDED.captions_default,
    theater_default  = EXCLUDED.theater_default,
    video_card_previews_enabled = EXCLUDED.video_card_previews_enabled,
    updated_at       = now();
