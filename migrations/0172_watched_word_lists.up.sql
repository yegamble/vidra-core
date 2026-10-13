-- 0172: named watched-word lists (PeerTube 6.2 parity). The flat instance
-- watched_words table becomes one list among several, at two levels:
--   * instance lists (owner_id NULL), managed by moderators/admins;
--   * per-account lists (owner_id = the creator), managed by that creator and
--     applied ONLY to comments on that creator's own videos.
-- Each list carries an action: 'flag' (record a match for review, the
-- behaviour every term had until now) or 'hold' (additionally park the comment
-- in the existing held_for_review queue, 0161 — no second hold path).
CREATE TABLE watched_word_lists (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    owner_id   UUID REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 100),
    action     TEXT NOT NULL DEFAULT 'flag' CHECK (action IN ('flag', 'hold')),
    -- The one list the legacy flat endpoints (/admin/watched-words) address.
    -- It cannot be deleted, and it is always an instance list.
    is_default BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (NOT is_default OR owner_id IS NULL)
);
CREATE UNIQUE INDEX watched_word_lists_instance_name_idx ON watched_word_lists (lower(name)) WHERE owner_id IS NULL;
CREATE UNIQUE INDEX watched_word_lists_owner_name_idx ON watched_word_lists (owner_id, lower(name)) WHERE owner_id IS NOT NULL;
CREATE UNIQUE INDEX watched_word_lists_default_idx ON watched_word_lists (is_default) WHERE is_default;

INSERT INTO watched_word_lists (name, is_default) VALUES ('Default', true);

-- Every existing term moves, unchanged, into the default instance list.
ALTER TABLE watched_words ADD COLUMN list_id UUID REFERENCES watched_word_lists (id) ON DELETE CASCADE;
UPDATE watched_words SET list_id = (SELECT id FROM watched_word_lists WHERE is_default);
-- list_id stays nullable at the column level (migrate-lint forbids tightening nullability:
-- the previous release must keep inserting); the trigger below makes NULL
-- unreachable instead.

-- A term is unique per list, not globally (two lists may both watch "spam").
DROP INDEX watched_words_word_lower_idx;
CREATE UNIQUE INDEX watched_words_list_word_lower_idx ON watched_words (list_id, lower(word));

-- An INSERT that names no list (the previous release, which predates lists)
-- lands in the default list, so an app-only rollback keeps working.
CREATE FUNCTION watched_words_default_list() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.list_id IS NULL THEN
        NEW.list_id := (SELECT id FROM watched_word_lists WHERE is_default);
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER watched_words_default_list BEFORE INSERT ON watched_words
    FOR EACH ROW EXECUTE FUNCTION watched_words_default_list();

-- Which list matched. list_name is a snapshot (like matched_term, 0132): the
-- match outlives a deleted list and still says where it came from.
ALTER TABLE watched_word_matches
    ADD COLUMN list_id UUID REFERENCES watched_word_lists (id) ON DELETE SET NULL,
    ADD COLUMN list_name TEXT NOT NULL DEFAULT '',
    -- Owner of the list at match time (NULL = instance list). The staff review
    -- queue shows list_owner_id IS NULL only: a creator's private terms must not
    -- reach moderators. Cascades with the account.
    ADD COLUMN list_owner_id UUID REFERENCES users (id) ON DELETE CASCADE;
UPDATE watched_word_matches m
SET list_id = l.id, list_name = l.name
FROM watched_words w
JOIN watched_word_lists l ON l.id = w.list_id
WHERE w.id = m.watched_word_id;
