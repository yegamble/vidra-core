-- 0155: bound open remote reports per target — at most one OPEN remote (Flag)
-- report per (reporting domain, target). CreateRemoteFlagReport's ON CONFLICT
-- DO NOTHING turns a repeat into no row, so nothing is filed or notified; once
-- that report is resolved the domain can report the target again.
CREATE UNIQUE INDEX reports_remote_open_target_idx
    ON reports (remote_reporter_domain, target_type, COALESCE(video_id, comment_id, reported_user_id))
    WHERE remote_reporter_domain IS NOT NULL AND status = 'open';
