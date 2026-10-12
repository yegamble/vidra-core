-- 0152: remote_videos.is_sensitive (T05). An inbound Create/Update{Video}'s
-- ActivityStreams `sensitive` flag (PeerTube's nsfw) was read by nothing, so a
-- remote video the origin flagged played with no warning whatever the viewer's
-- sensitive-content policy said. Stored like videos.is_sensitive (0082) so the
-- same policy applies to both. Existing rows default false until the origin's
-- next Update refreshes them — the honest answer for a flag never recorded.
ALTER TABLE remote_videos ADD COLUMN is_sensitive BOOLEAN NOT NULL DEFAULT false;
