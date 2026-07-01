-- 0023: Watched words. A moderator/admin maintains an instance-wide list of terms;
-- content (comments, and later videos) containing a listed term can be flagged or
-- held for review. This migration plus the add/list/delete endpoints are the list
-- management surface — the matching/flagging effect on content is a later slice
-- (with its own matches table).
CREATE TABLE watched_words (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    word       TEXT NOT NULL,
    created_by UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One entry per term, case-insensitive.
CREATE UNIQUE INDEX watched_words_word_lower_idx ON watched_words (lower(word));
