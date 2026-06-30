-- 0020: Abuse reports. A user reports a video or a comment with a reason;
-- moderators/admins triage them (accept/reject with an internal note). The
-- nullable video_id / comment_id carry the target matching target_type. A user
-- can report a given target at most once (partial unique indexes).
CREATE TABLE reports (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    reporter_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    target_type    TEXT NOT NULL CHECK (target_type IN ('video', 'comment')),
    video_id       UUID REFERENCES videos (id) ON DELETE CASCADE,
    comment_id     UUID REFERENCES comments (id) ON DELETE CASCADE,
    reason         TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'accepted', 'rejected')),
    moderator_note TEXT NOT NULL DEFAULT '',
    resolved_by    UUID REFERENCES users (id) ON DELETE SET NULL,
    resolved_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Moderation queue: newest reports first, filterable by status.
CREATE INDEX reports_status_created_idx ON reports (status, created_at DESC);
-- A user reports any given target at most once.
CREATE UNIQUE INDEX reports_reporter_video_idx ON reports (reporter_id, video_id) WHERE video_id IS NOT NULL;
CREATE UNIQUE INDEX reports_reporter_comment_idx ON reports (reporter_id, comment_id) WHERE comment_id IS NOT NULL;
