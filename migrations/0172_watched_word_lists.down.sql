-- 0172 down: back to the flat instance list. Per-account lists have no flat
-- equivalent, so their terms are discarded; instance lists are merged (a term
-- present in several keeps its oldest row, since the flat table is unique per
-- term). Matches survive: watched_word_id is ON DELETE SET NULL (0132) and the
-- snapshot carries the term.
ALTER TABLE watched_word_matches DROP COLUMN list_id, DROP COLUMN list_name;

DROP TRIGGER watched_words_default_list ON watched_words;
DROP FUNCTION watched_words_default_list();

DELETE FROM watched_words
WHERE list_id IN (SELECT id FROM watched_word_lists WHERE owner_id IS NOT NULL);
DELETE FROM watched_words w
USING watched_words d
WHERE lower(w.word) = lower(d.word) AND (d.created_at, d.id) < (w.created_at, w.id);

DROP INDEX watched_words_list_word_lower_idx;
ALTER TABLE watched_words DROP COLUMN list_id;
CREATE UNIQUE INDEX watched_words_word_lower_idx ON watched_words (lower(word));

DROP TABLE watched_word_lists;
