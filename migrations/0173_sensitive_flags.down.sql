DROP FUNCTION IF EXISTS vidra_sensitive_hidden(boolean, text[], boolean, uuid);
DROP TABLE IF EXISTS user_content_prefs;
ALTER TABLE remote_videos DROP COLUMN IF EXISTS sensitive_flags;
ALTER TABLE videos DROP COLUMN IF EXISTS sensitive_flags;
