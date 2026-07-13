-- Roll back 0095_video_card_previews.
ALTER TABLE user_player_settings
    DROP COLUMN video_card_previews_enabled;
