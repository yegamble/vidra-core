-- 0030: Watched-word matches. When a comment is posted, the moderation surface
-- checks it against the instance watched-words list and records a row here for
-- each term found, so moderators can review flagged comments. This slice detects
-- and records only; auto-holding/hiding the content is a later slice (needs a
-- product decision on the hold/hide behavior).
CREATE TABLE watched_word_matches (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    watched_word_id UUID NOT NULL REFERENCES watched_words (id) ON DELETE CASCADE,
    comment_id      UUID NOT NULL REFERENCES comments (id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- A comment matches a given term at most once.
    UNIQUE (watched_word_id, comment_id)
);

-- The moderation review queue: newest match first.
CREATE INDEX watched_word_matches_created_idx ON watched_word_matches (created_at DESC);
