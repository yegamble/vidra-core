DROP INDEX IF EXISTS comments_parent_id_idx;
ALTER TABLE comments DROP COLUMN IF EXISTS parent_id;
