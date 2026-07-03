-- 0044: free-form video tags (product-decisions.md §18). Tags are lowercased
-- and trimmed by the service before insert; at most 5 per video (enforced in
-- the application layer, where the count can be validated per request). The
-- composite PK dedupes per video; the tag index serves the ?tag= feed filter
-- and search matching.
CREATE TABLE video_tags (
    video_id   UUID        NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    tag        TEXT        NOT NULL CHECK (tag = lower(tag) AND length(tag) BETWEEN 1 AND 50),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (video_id, tag)
);

CREATE INDEX video_tags_tag_idx ON video_tags (tag);
