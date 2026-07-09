-- Video-level sensitive-content flag (spec instance-platform-info). Owner-set
-- on create/update; when the instance's sensitive_content_policy is "hide",
-- flagged videos are excluded from the PUBLIC browse/search surfaces (owner
-- studio listings, admin surfaces, and direct watch-by-id stay unfiltered).
ALTER TABLE videos ADD COLUMN is_sensitive BOOLEAN NOT NULL DEFAULT false;
