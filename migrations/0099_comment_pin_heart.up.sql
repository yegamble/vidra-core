-- 0099: creator pin + heart (YouTube-style). Two local, non-federated creator
-- interactions on a video's comments:
--   * hearted — the video owner "hearts" any number of comments (a like from the
--     creator). Works on remote-authored comments too (local metadata only).
--   * pinned_comment_id — the video owner pins AT MOST ONE top-level comment to
--     the top of the thread. A single column holds the pin, so setting it
--     atomically replaces any prior pin; ON DELETE SET NULL clears the pin if the
--     pinned comment is hard-deleted.
-- Neither federates: they carry no ActivityPub side effect and never touch the
-- comment body/updated_at (so the "edited" marker is unaffected).
ALTER TABLE comments ADD COLUMN hearted BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE videos ADD COLUMN pinned_comment_id UUID REFERENCES comments (id) ON DELETE SET NULL;
