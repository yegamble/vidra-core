-- Revert 0161. Fails CLOSED on both halves of the feature:
--   * a held comment was never public, and dropping held_for_review would
--     publish every one of them at once — so held comments are removed first;
--   * a 'requires_approval' video becomes 'disabled' rather than 'enabled', the
--     same fail-closed mapping the PeerTube importer applies to an approval
--     policy it cannot represent.
DROP INDEX IF EXISTS notifications_comment_held_unique_idx;
DELETE FROM comments WHERE held_for_review;
DROP INDEX IF EXISTS comments_held_for_review_idx;
ALTER TABLE comments DROP COLUMN IF EXISTS held_for_review;
UPDATE videos SET comments_policy = 'disabled' WHERE comments_policy = 'requires_approval';
ALTER TABLE videos DROP CONSTRAINT videos_comments_policy_check;
ALTER TABLE videos ADD CONSTRAINT videos_comments_policy_check
    CHECK (comments_policy IN ('enabled', 'disabled'));
