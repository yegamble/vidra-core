-- 0041: per-user storage quota (P9 user edit + upload enforcement). NULL means
-- the account inherits the instance default (INSTANCE_DEFAULT_QUOTA_BYTES); a
-- value of 0 means unlimited for that account (mirroring the instance-level
-- semantics where 0/unset = unlimited); a positive value caps the total bytes
-- of the user's stored video files (originals + renditions + thumbnails across
-- the videos owned via their channels). Usage is computed live by aggregating
-- video_files — no denormalized counter.

ALTER TABLE users
    ADD COLUMN storage_quota_bytes BIGINT
        CHECK (storage_quota_bytes IS NULL OR storage_quota_bytes >= 0);
