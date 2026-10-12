-- 0156: staff push signals for the three approval queues that used to fill up
-- silently — new registration requests (approval mode), uploads parked by the
-- quarantine gate, and remote follower requests (federation_follower_approval).
-- Before this the admin only saw a pull-only pending-count badge, so an
-- approval-mode instance accumulated unseen queues. Mirrors 0103 (new_report):
--
--   * two nullable subject columns, because neither a registration request nor
--     a remote follow had a home on notifications (upload_quarantined reuses
--     the existing video_id). ON DELETE CASCADE: a notice about a row that no
--     longer exists has nothing to point at.
--   * one partial unique index per type makes "at most one notification per
--     (recipient, subject)" a DATABASE guarantee, so a retried request or a
--     re-fired hook can never double-notify. Each is scoped to its own type.
--
-- notifications.type is unconstrained TEXT, so no enum/check change is needed.
ALTER TABLE notifications
    ADD COLUMN registration_request_id UUID REFERENCES registration_requests (id) ON DELETE CASCADE,
    ADD COLUMN remote_follow_id        UUID REFERENCES remote_follows (id) ON DELETE CASCADE;

CREATE UNIQUE INDEX notifications_new_registration_request_unique_idx
    ON notifications (user_id, registration_request_id)
    WHERE type = 'new_registration_request';
CREATE UNIQUE INDEX notifications_upload_quarantined_unique_idx
    ON notifications (user_id, video_id)
    WHERE type = 'upload_quarantined';
CREATE UNIQUE INDEX notifications_federation_follower_request_unique_idx
    ON notifications (user_id, remote_follow_id)
    WHERE type = 'federation_follower_request';
