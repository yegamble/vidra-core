-- 0147: locally-authored comments ON remote videos — the home-instance-hosts-and-
-- federates model.
--
-- WHAT THIS REVERSES, AND ON WHOSE AUTHORITY. Migration 0140 (remote_video_comments)
-- is a READ-ONLY MIRROR of a thread the origin already fans out to us; it has no
-- user_id column, and its comment states that authoring a comment about a video
-- this instance does not host was DEFERRED as a product ruling — "who moderates a
-- comment an instance hosts about a video it does not own". The owner has now made
-- that ruling: the AUTHOR'S HOME INSTANCE hosts and moderates the locally-authored
-- comment, and federates it to the origin as a Create{Note} inReplyTo the remote
-- video. This table is where the home instance keeps them. The mirror table stays
-- exactly as it was — read-only, no user_id — because these are a DIFFERENT KIND of
-- thing: a mirror carries somebody else's actor and nothing here may write it; a
-- row HERE carries a LOCAL author this instance owns, moderates, and answers for.
--
-- WHY A SEPARATE TABLE AND NOT `comments`. `comments.video_id` is NOT NULL
-- REFERENCES videos and is the most-joined column in the schema; a comment about a
-- video this instance does not host has no local video to point at. Overloading
-- `comments` would put a NULL check into every moderation, notification,
-- watched-word and listing query — the same reasoning 0140 gives for the mirror.
CREATE TABLE authored_remote_comments (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    -- The remote video this comment is about. CASCADE matches remote_videos'
    -- own cascade: if the origin retracts the video (or its actor is deleted),
    -- the local thread about it goes too — there is nothing left to reply to.
    remote_video_id UUID        NOT NULL REFERENCES remote_videos (id) ON DELETE CASCADE,
    -- The LOCAL author. CASCADE: a deleted account takes its authored comments
    -- with it, the same way a local account deletion is handled for its content.
    user_id         UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    body            TEXT        NOT NULL,
    -- The ActivityPub object id this instance MINTS for the comment
    -- (baseURL + /remote-comments/<id>), dereferenceable and unique. It is the id
    -- an Update or Delete we emit names, and the id a peer would fetch to verify.
    object_url      TEXT        NOT NULL UNIQUE,
    -- The origin video's ActivityPub object id — the inReplyTo of the Create{Note}
    -- we deliver. Snapshotted at author time from remote_videos.object_url so the
    -- reply threads onto the video on the origin exactly as a same-origin reply
    -- would.
    in_reply_to     TEXT        NOT NULL,
    -- Federation status, shown to the author so they can tell "written here" from
    -- "accepted there": 'pending' until the delivery queue lands it, 'delivered'
    -- once the origin inbox answered 2xx, 'failed' when the delivery dead-lettered
    -- or was cancelled (destination blocked). The comment is DISPLAYED LOCALLY
    -- regardless — the home instance hosts it — this only reports the federation leg.
    delivery_state  TEXT        NOT NULL DEFAULT 'pending'
                        CHECK (delivery_state IN ('pending', 'delivered', 'failed')),
    -- The last delivery error, mirrored from the queue row for the author's view.
    last_error      TEXT        NOT NULL DEFAULT '',
    -- Delivery attempts made, mirrored from the queue row.
    attempts        INT         NOT NULL DEFAULT 0,
    -- True once the body has been edited (and an Update{Note} re-federated).
    edited          BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The thread read: one remote video's locally-authored comments, oldest first
-- (the order a thread is written in), the same order the mirror renders.
CREATE INDEX authored_remote_comments_video_idx
    ON authored_remote_comments (remote_video_id, created_at);
-- Everything one local user authored, for the author's own management + the
-- viewer mute filter.
CREATE INDEX authored_remote_comments_user_idx
    ON authored_remote_comments (user_id);

-- Let an outbound delivery point back at the authored comment it carries, so the
-- drain can reflect the delivery result onto delivery_state without hand-rolling a
-- second delivery path. NULL for every other delivery (video/comment fan-out,
-- follows). ON DELETE SET NULL: an author's delete removes the comment row while
-- its in-flight Delete activity is still queued — the queue row must survive to be
-- sent, it simply no longer has a comment to update.
ALTER TABLE federation_deliveries
    ADD COLUMN authored_remote_comment_id UUID
        REFERENCES authored_remote_comments (id) ON DELETE SET NULL;

-- Moderation is the home instance's job (the ruling): an authored remote comment's
-- body is checked against the watched-words list exactly as a local comment's is,
-- and its matches surface in the same review queue. A third nullable target beside
-- comment_id and video_id, under the same ON DELETE CASCADE (a match goes with its
-- target), and the one-target CHECK widened to admit it.
ALTER TABLE watched_word_matches
    ADD COLUMN authored_remote_comment_id UUID
        REFERENCES authored_remote_comments (id) ON DELETE CASCADE;

-- Widen the exactly-one-target CHECK (the drop-then-re-add-same-name idiom
-- migrate-lint permits): a match now flags exactly one of a comment, a video, or
-- an authored remote comment.
ALTER TABLE watched_word_matches DROP CONSTRAINT watched_word_matches_one_target;
ALTER TABLE watched_word_matches ADD CONSTRAINT watched_word_matches_one_target
    CHECK (
        (comment_id IS NOT NULL)::int
      + (video_id IS NOT NULL)::int
      + (authored_remote_comment_id IS NOT NULL)::int
      = 1
    );

-- An authored remote comment matches a given term at most once (partial, mirroring
-- the comment and video unique indexes).
CREATE UNIQUE INDEX watched_word_matches_authored_remote_uniq
    ON watched_word_matches (watched_word_id, authored_remote_comment_id)
    WHERE authored_remote_comment_id IS NOT NULL;
