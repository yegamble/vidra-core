-- Remote-authored comments cannot survive without their attribution columns.
DELETE FROM comments WHERE remote_actor_url IS NOT NULL;

DROP INDEX IF EXISTS comments_remote_actor_idx;
ALTER TABLE comments
    DROP CONSTRAINT comments_exactly_one_author_check,
    DROP COLUMN remote_object_url,
    DROP COLUMN remote_author_name,
    DROP COLUMN remote_actor_url,
    ALTER COLUMN user_id SET NOT NULL;
