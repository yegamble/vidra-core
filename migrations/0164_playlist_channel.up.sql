-- 0164: an optional channel on a playlist. A playlist stays user-owned (owner_id
-- is the authority for every permission check); channel_id only says "show this
-- on that channel's Playlists tab". ON DELETE SET NULL: deleting a channel must
-- not delete the owner's playlist, it just stops being attributed to it.
ALTER TABLE playlists
    ADD COLUMN channel_id UUID REFERENCES channels (id) ON DELETE SET NULL;

-- Listing a channel's playlists newest-first is the hot path. Partial: most
-- playlists carry no channel.
CREATE INDEX playlists_channel_created_idx
    ON playlists (channel_id, created_at DESC, id DESC)
    WHERE channel_id IS NOT NULL;
