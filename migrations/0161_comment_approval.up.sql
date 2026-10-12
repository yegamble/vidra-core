-- 0161: comment approval — PeerTube's third comments policy, deferred by 0089
-- because no approval queue existed. A video whose comments_policy is
-- 'requires_approval' still ACCEPTS comments, but a comment from anyone other
-- than the people who manage the video (its channel owner, an editor member)
-- or staff is stored HELD: visible only to its author, the video's managers
-- and staff, never federated, and never announced to a third party, until a
-- manager approves it (POST /videos/{id}/comments/{comment_id}/approve) or
-- deletes it. Approval is the one-way transition held_for_review true -> false;
-- a comment is never re-held, so turning the policy off later leaves already
-- held comments waiting rather than publishing them silently.
--
-- Widen-a-CHECK idiom (drop, then re-add the same name): the previous release
-- only ever writes 'enabled'/'disabled', which the wider check still accepts.
ALTER TABLE videos DROP CONSTRAINT videos_comments_policy_check;
ALTER TABLE videos ADD CONSTRAINT videos_comments_policy_check
    CHECK (comments_policy IN ('enabled', 'disabled', 'requires_approval'));

-- Every existing comment was posted under a policy that published it, so the
-- default false is the truthful backfill, and an insert by the previous
-- release (which does not name the column) stays visible exactly as before.
ALTER TABLE comments ADD COLUMN held_for_review boolean NOT NULL DEFAULT false;

-- The managers' review queue reads "held comments on these videos, newest
-- first". Held rows are a small, transient minority, so a partial index keeps
-- the queue cheap without taxing every published comment.
CREATE INDEX comments_held_for_review_idx
    ON comments (video_id, created_at DESC)
    WHERE held_for_review;

-- comment_held tells a video's managers that a comment is waiting for them —
-- a set-based fan-out (the 0101/0103 idiom). One notification per (recipient,
-- comment) is a DATABASE guarantee, so a re-delivered federated Note or a
-- retried hook can never double-notify. notifications.type is unconstrained
-- TEXT, so no check change is needed for the new type.
CREATE UNIQUE INDEX notifications_comment_held_unique_idx
    ON notifications (user_id, comment_id)
    WHERE type = 'comment_held';
