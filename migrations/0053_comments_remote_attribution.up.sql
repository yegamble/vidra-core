-- 0053: Federated comment attribution (.ralph/specs/federation-remote-content.md §6).
-- A comment is now authored EITHER by a local user (user_id) OR by a remote
-- ActivityPub actor (remote_actor_url + a display-name snapshot) — exactly one.
-- remote_object_url is the remote Note's ActivityPub object id: the dedupe key
-- for re-delivered Create{Note}s and the lookup key for inbound Update{Note} /
-- Delete. Deleting a cached remote actor cascades away its comments (§7's
-- actor-Delete). Local comments keep user_id semantics unchanged.
ALTER TABLE comments
    ALTER COLUMN user_id DROP NOT NULL,
    ADD COLUMN remote_actor_url   TEXT REFERENCES remote_actors (actor_url) ON DELETE CASCADE,
    ADD COLUMN remote_author_name TEXT,
    ADD COLUMN remote_object_url  TEXT UNIQUE,
    ADD CONSTRAINT comments_exactly_one_author_check
        CHECK ((user_id IS NULL) <> (remote_actor_url IS NULL));

-- The §7 actor-Delete cascade (and per-actor moderation lookups) walk this FK.
CREATE INDEX comments_remote_actor_idx ON comments (remote_actor_url)
    WHERE remote_actor_url IS NOT NULL;
