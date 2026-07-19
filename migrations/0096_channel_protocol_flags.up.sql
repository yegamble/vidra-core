-- 0096: Per-channel protocol distribution flags (studio "Distribution" surface).
-- A channel may publish over ActivityPub, over ATProto/Bluesky, or both. Both
-- default TRUE so existing channels keep federating exactly as before; a creator
-- opts a channel out per protocol from the studio Channel tab (owner only).
--
-- Enforcement (see internal/federation + internal/atproto):
--   * activitypub_enabled=false → inbound Follow ignored, outbound Create/
--     Update/Delete/Announce skipped for the channel.
--   * atproto_enabled=false     → the channel's published videos are never
--     enqueued to atproto_posts (no Bluesky cross-post).

ALTER TABLE channels
    ADD COLUMN activitypub_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN atproto_enabled     BOOLEAN NOT NULL DEFAULT TRUE;
