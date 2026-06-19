-- 0007: trigram index on video titles to back fuzzy title search
-- (GET /api/v1/videos/search). pg_trgm is enabled in 0001.

CREATE INDEX videos_title_trgm_idx ON videos USING gin (title gin_trgm_ops);
