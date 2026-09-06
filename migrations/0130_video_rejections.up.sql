-- 0130: the moderator's rejection note (A16 hardening). POST
-- /admin/videos/{id}/reject has always accepted a `reason`, and the quarantine
-- UI told the moderator it was "recorded in the audit trail" — but nothing
-- stored it: the audit envelope deliberately carries only the classification
-- `reason_provided: true|false` (free-form prose can contain PII and must never
-- enter audit_log), and no other column held it either, so the note was
-- discarded the moment the handler returned.
--
-- This is the moderation-domain home for that prose, shaped exactly like
-- video_blocks (0021): one row per video, the acting moderator, the moment. It
-- is what the creator is shown on their video_rejected notification (the whole
-- point of asking a moderator to write it) and what staff read back on the
-- video's moderation-inventory row. It is NOT an audit record and must not be
-- used as one.
CREATE TABLE video_rejections (
    video_id    UUID PRIMARY KEY REFERENCES videos (id) ON DELETE CASCADE,
    note        TEXT NOT NULL DEFAULT '',
    rejected_by UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
