-- 0026: Comment threading. A comment may reply to another comment on the SAME
-- video via parent_id (the column reserved by 0014). Top-level comments have
-- parent_id NULL. Deleting a parent cascades to its replies, consistent with the
-- existing hard-delete comment model; a "soft-delete that keeps the thread
-- visible" is a later refinement (PT-COMMENTS).
ALTER TABLE comments
    ADD COLUMN parent_id UUID REFERENCES comments (id) ON DELETE CASCADE;

-- Fetching a comment's replies (and, later, listing only top-level threads) is
-- the hot path; index the non-null parents so it stays cheap.
CREATE INDEX comments_parent_id_idx ON comments (parent_id) WHERE parent_id IS NOT NULL;
