-- Roll back 0159_video_privacy_internal. Internal videos become PRIVATE — the
-- stricter tier, so a rollback never widens who can see one — before the
-- narrower CHECK is restored (it would reject any row still at 'internal').
UPDATE videos SET privacy = 'private' WHERE privacy = 'internal';

ALTER TABLE videos DROP CONSTRAINT videos_privacy_check;
ALTER TABLE videos ADD CONSTRAINT videos_privacy_check
    CHECK (privacy IN ('public', 'unlisted', 'private', 'password'));
