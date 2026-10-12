-- 0153: reports another instance filed via an ActivityPub Flag (T07) have no
-- local reporter: the remote actor, domain and Flag id instead, exactly one kind
-- per row. Release N-1 always writes reporter_id and INNER JOINs users in its
-- queue, so a remote report is invisible to it rather than unreadable.
ALTER TABLE reports ALTER COLUMN reporter_id DROP NOT NULL;
ALTER TABLE reports
    ADD COLUMN remote_reporter_actor_url TEXT,
    ADD COLUMN remote_reporter_domain    TEXT,
    ADD COLUMN remote_flag_activity_id   TEXT;
ALTER TABLE reports ADD CONSTRAINT reports_reporter_kind_check CHECK (
    (reporter_id IS NOT NULL AND remote_reporter_actor_url IS NULL)
    OR (reporter_id IS NULL AND remote_reporter_actor_url IS NOT NULL
        AND remote_reporter_domain IS NOT NULL AND remote_flag_activity_id IS NOT NULL));
-- Idempotency: one report per (Flag, target). Keyed on the Flag, not the actor:
-- one instance actor reports on behalf of many people.
CREATE UNIQUE INDEX reports_remote_flag_target_idx
    ON reports (remote_flag_activity_id, target_type, COALESCE(video_id, comment_id, reported_user_id))
    WHERE remote_flag_activity_id IS NOT NULL;
