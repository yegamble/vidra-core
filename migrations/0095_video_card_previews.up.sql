-- 0095: Per-user video-card preview preference.
--
-- The instance-level feature gate and inherited user default live in the generic
-- instance_settings table under video_card_previews_enabled and
-- video_card_previews_default_enabled. This nullable column is an EXPLICIT user
-- override. NULL means "inherit the current admin default"; FALSE and TRUE mean
-- the user chose that value and later admin-default changes must not replace it.
-- The global video_card_previews_enabled gate still applies in every case.
ALTER TABLE user_player_settings
    ADD COLUMN video_card_previews_enabled BOOLEAN;
