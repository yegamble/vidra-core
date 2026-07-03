-- 0048: upload quarantine pipeline (product-decisions.md §11). When the
-- QUARANTINE_NEW_UPLOADS instance setting is on, a finished upload by a
-- non-privileged user (role = 'user' with bypass_quarantine = false) parks in
-- the new 'quarantined' state instead of publishing. Quarantined videos are
-- visible to the owner (badged) and moderators only; every public surface
-- already filters on state = 'published', so they never appear there. A
-- moderator approves (-> published, through the real publish transition so the
-- federation-announce + transcode hooks fire at that moment, not at upload
-- time) or rejects (-> failed, owner notified).
ALTER TABLE users ADD COLUMN bypass_quarantine BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE videos DROP CONSTRAINT videos_state_check;
ALTER TABLE videos ADD CONSTRAINT videos_state_check
    CHECK (state IN ('draft', 'processing', 'scheduled', 'quarantined', 'published', 'failed'));

-- The moderation queue scan: only quarantined rows, newest upload first.
CREATE INDEX videos_quarantined_idx ON videos (created_at DESC) WHERE state = 'quarantined';
