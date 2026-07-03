-- 0054: Remote-video moderation (.ralph/specs/federation-remote-content.md §8).
--
-- Reports gain target_type='remote_video': a signed-in user can report an
-- ingested remote video locally, alongside the existing video/comment/account
-- targets. remote_video_id carries the target; one report per reporter+target.
ALTER TABLE reports DROP CONSTRAINT IF EXISTS reports_target_type_check;
ALTER TABLE reports ADD CONSTRAINT reports_target_type_check
    CHECK (target_type IN ('video', 'comment', 'account', 'remote_video'));

ALTER TABLE reports ADD COLUMN remote_video_id UUID REFERENCES remote_videos (id) ON DELETE CASCADE;

CREATE UNIQUE INDEX reports_reporter_remote_video_idx
    ON reports (reporter_id, remote_video_id) WHERE remote_video_id IS NOT NULL;

-- remote_video_blocks: the admin per-video hide for remote content, mirroring
-- video_blocks. A blocked remote video vanishes from the subscriptions feed,
-- search, the scope=all discovery feed, and the remote-watch detail endpoint.
-- Unblocking restores it. One row per blocked remote video; the reason and the
-- acting moderator are recorded for the audit trail.
CREATE TABLE remote_video_blocks (
    remote_video_id UUID        PRIMARY KEY REFERENCES remote_videos (id) ON DELETE CASCADE,
    reason          TEXT        NOT NULL DEFAULT '',
    blocked_by      UUID        REFERENCES users (id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
