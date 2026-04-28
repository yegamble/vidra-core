-- +goose Up
-- +goose StatementBegin
-- Phase 11 — per-video ATProto cross-post target.
-- atproto_uri stores the at:// URI of the Bluesky post created when a user syndicates
-- this video. NULL for non-syndicated videos. UNIQUE partial index prevents two
-- videos sharing the same AT URI (race-condition protection per Phase 11 plan F4).
ALTER TABLE videos
ADD COLUMN IF NOT EXISTS atproto_uri TEXT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_videos_atproto_uri
    ON videos(atproto_uri)
    WHERE atproto_uri IS NOT NULL;

COMMENT ON COLUMN videos.atproto_uri IS 'AT URI (at://did:plc:.../app.bsky.feed.post/...) of the Bluesky post for this video. NULL when not syndicated.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Dev-only rollback. Production rolls forward, never backward — clearing atproto_uri
-- values would silently re-fillable on next syndicate, which is fine, but the column
-- itself should not be dropped without coordinating with frontend Video.atproto_uri.
DROP INDEX IF EXISTS uq_videos_atproto_uri;
ALTER TABLE videos DROP COLUMN IF EXISTS atproto_uri;
-- +goose StatementEnd
