-- 0140: a follower instance keeps the comment thread it is already being sent.
--
-- A29 measured this one end to end and it is worth restating, because the
-- failure was silent on both sides: A's creator replied to their own video, the
-- reply fanned out as Create{Note} to all three follower inboxes, all three
-- deliveries succeeded — and B stored ZERO comments. resolveNoteTarget requires
-- inReplyTo to resolve to a LOCAL video, so a follower receives every federated
-- comment for the videos it follows and drops every one. A remote video carried
-- no thread anywhere but its origin, and the delivery that would have populated
-- one was recorded as delivered.
--
-- WHY A SEPARATE TABLE AND NOT comments.video_id GOING NULLABLE. `comments` is
-- the most-joined table in the schema, its video_id is NOT NULL REFERENCES
-- videos, and every moderation, notification, watched-word and listing query
-- reads it. Making that column nullable to admit rows about a video this
-- instance does not host would put a NULL check into all of them and change the
-- meaning of a table that currently has exactly one. These rows are also a
-- different KIND of thing: they are a MIRROR of somebody else's thread, they
-- carry no local author, and nothing here may edit them — the origin is the
-- only writer.
--
-- WHAT THIS TABLE DELIBERATELY CANNOT REPRESENT: a comment authored HERE about a
-- remote video. There is no user_id column, and that absence is the design. The
-- shipped product decision is that "comments, ratings and saving live on the
-- origin instance", and reversing it is a product ruling — one that also has to
-- answer who moderates a comment an instance hosts about a video it does not
-- own. Mirroring a thread the origin already fans out to us asks none of those
-- questions: it is the same class of content as the remote video row itself,
-- under the same controls (instance mute, instance block, per-remote-account
-- block, per-video block, reports).
CREATE TABLE remote_video_comments (
    id                 UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    remote_video_id    UUID        NOT NULL REFERENCES remote_videos (id) ON DELETE CASCADE,
    -- The commenter's actor. FK to the cache because the ingest resolves and
    -- caches the signer during signature verification, so the row always exists
    -- by the time a Note is stored — and deleting an actor takes its comments
    -- with it, matching how remote_videos already cascades.
    remote_actor_url   TEXT        NOT NULL REFERENCES remote_actors (actor_url) ON DELETE CASCADE,
    -- A display-name SNAPSHOT, like comments.remote_author_name: the thread must
    -- still render when the actor row is gone or was never fetched.
    remote_author_name TEXT        NOT NULL DEFAULT '',
    -- The ActivityPub object id on the ORIGIN. It is the dedupe key (a redelivery
    -- must not double the thread) and the address an Update or Delete names.
    object_url         TEXT        NOT NULL UNIQUE,
    body               TEXT        NOT NULL,
    edited             BOOLEAN     NOT NULL DEFAULT FALSE,
    published_at       TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The thread read: one video's comments, oldest first.
CREATE INDEX remote_video_comments_video_idx
    ON remote_video_comments (remote_video_id, created_at);
-- Everything one actor said, for the per-remote-account block filter.
CREATE INDEX remote_video_comments_actor_idx
    ON remote_video_comments (remote_actor_url);
