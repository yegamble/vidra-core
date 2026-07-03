-- 0042: Let a notification point at an abuse report, so a "report_resolved"
-- notification (a moderator accepted/rejected your report) can carry which
-- report it was. Nullable and type-dependent like the other context columns;
-- cascades if the report row is deleted. The moderator's identity is
-- deliberately NOT stored (actor_id stays NULL for this type) so it is never
-- exposed to the reporter.
ALTER TABLE notifications
    ADD COLUMN report_id UUID REFERENCES reports (id) ON DELETE CASCADE;
