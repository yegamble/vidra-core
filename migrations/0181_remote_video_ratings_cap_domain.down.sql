DROP INDEX IF EXISTS remote_video_ratings_cap_domain_created_idx;
ALTER TABLE remote_video_ratings DROP COLUMN IF EXISTS cap_domain;
