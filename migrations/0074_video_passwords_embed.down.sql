-- Roll back 0074_video_passwords_embed.
DROP TABLE IF EXISTS video_passwords;

ALTER TABLE videos DROP COLUMN IF EXISTS embed_allowed_domains;
ALTER TABLE videos DROP COLUMN IF EXISTS embed_privacy;

-- Restore the original privacy set (any rows still at 'password' would violate
-- this; the rollback assumes they were migrated back first, matching the
-- state-check rollback convention in 0045/0048).
ALTER TABLE videos DROP CONSTRAINT videos_privacy_check;
ALTER TABLE videos ADD CONSTRAINT videos_privacy_check
    CHECK (privacy IN ('public', 'unlisted', 'private'));
