-- 0163: a per-report message thread between the reporter and staff (PeerTube's
-- abuse messages). Before this a reporter heard back exactly once — the
-- report_resolved notice — and had no way to add context or ask what happened,
-- and staff had no way to ask the reporter a question.
--
--   * author_id is SET NULL on account deletion so a departed moderator's
--     messages stay in the thread; the reporter cannot vanish that way, because
--     deleting the reporter cascades the report itself (0020).
--   * is_moderator is fixed at write time (the author was not the reporter), so
--     a later role change never rewrites which side of the thread said what.
--   * the body is bounded in the database, not just the handler.
--
-- Numbered 0163: 0159–0162 were claimed by PRs developed in parallel.
CREATE TABLE report_messages (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    report_id    UUID NOT NULL REFERENCES reports (id) ON DELETE CASCADE,
    author_id    UUID REFERENCES users (id) ON DELETE SET NULL,
    is_moderator BOOLEAN NOT NULL,
    body         TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 3000),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX report_messages_report_created_idx ON report_messages (report_id, created_at);

-- The report_message notification (the 0103/0156 pattern): one nullable subject
-- column, and a partial unique index that makes "at most one notification per
-- (recipient, message)" a DATABASE guarantee, so a retried fan-out never
-- double-notifies. Scoped to its own type.
ALTER TABLE notifications
    ADD COLUMN report_message_id UUID REFERENCES report_messages (id) ON DELETE CASCADE;
CREATE UNIQUE INDEX notifications_report_message_unique_idx
    ON notifications (user_id, report_message_id)
    WHERE type = 'report_message';
