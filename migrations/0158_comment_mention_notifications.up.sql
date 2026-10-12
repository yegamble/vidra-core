-- 0158: comment @mention notifications (merges after the 0157 PR).
--
-- A comment that @-mentions a local user now raises a 'comment_mention'
-- notification via a set-based fan-out (NotifyCommentMentions, the 0101/0103
-- pattern). notifications_comment_mention_unique_idx makes "at most one
-- comment_mention per (recipient, comment)" a DATABASE guarantee, so an edit
-- that re-states an existing mention, a retried request or a redelivered
-- federated Note can never notify twice. Scoped to the type so the types that
-- legitimately repeat per comment are untouched.
--
-- No new columns: notifications.type is unconstrained TEXT and
-- video_id/comment_id/actor_id already exist and are nullable.
CREATE UNIQUE INDEX notifications_comment_mention_unique_idx
    ON notifications (user_id, comment_id)
    WHERE type = 'comment_mention';
