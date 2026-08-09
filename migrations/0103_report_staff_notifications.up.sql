-- 0103: the "new abuse report" staff fan-out guard. Filing a report now pushes
-- a 'new_report' notification to every active admin/moderator (the moderation
-- queue's push signal — before this, staff only learned about reports by
-- loading an admin page). Mirrors the 0101 new_video pattern:
--
--   * notifications_new_report_unique_idx makes "at most one new_report
--     notification per (recipient, report)" a DATABASE guarantee rather than an
--     application convention, so a retried report request can never
--     double-notify. It is scoped to type = 'new_report' so the types that
--     legitimately repeat per report — the reporter's own report_resolved, for
--     instance — are untouched.
--
-- No new columns: notifications.type is unconstrained TEXT and report_id has
-- carried a nullable FK since 0042.
CREATE UNIQUE INDEX notifications_new_report_unique_idx
    ON notifications (user_id, report_id)
    WHERE type = 'new_report';
