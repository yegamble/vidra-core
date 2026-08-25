-- 0118 down: drop the display-name trigram indexes.
DROP INDEX IF EXISTS channels_display_name_trgm_idx;
DROP INDEX IF EXISTS users_display_name_trgm_idx;
