-- 0177: automatic tags on comments (PeerTube 6.2 "automatic tags"). Each local
-- comment carries the tags computed from its body when it was posted or edited:
-- 'external-link' (the text contains a URL) and the NAME of every INSTANCE
-- watched-word list it matches (0172). Moderators read and filter by them; the
-- matched WORDS are not stored here and never leave the staff surface.
--
-- Tags are a SNAPSHOT taken at post/edit time: renaming or deleting a list, or
-- adding words to one, does not retag existing comments.
--
-- A tag is a derived label, recomputed on every edit (the set is replaced), so
-- the table never needs a backfill: a comment predating this migration simply
-- has no tags until it is edited. ON DELETE CASCADE: a tag has no meaning
-- without its comment.
CREATE TABLE comment_automatic_tags (
    comment_id uuid NOT NULL REFERENCES comments (id) ON DELETE CASCADE,
    tag        text NOT NULL CHECK (char_length(tag) BETWEEN 1 AND 100),
    PRIMARY KEY (comment_id, tag)
);

-- The moderation filter reads "comments carrying this tag".
CREATE INDEX comment_automatic_tags_tag_idx ON comment_automatic_tags (lower(tag), comment_id);
