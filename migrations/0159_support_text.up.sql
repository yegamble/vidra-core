-- 0159: PeerTube's free-text "support" field (markdown, e.g. "Support me on
-- Liberapay: <link>") on channels and on videos. Numbered 0159: it
-- merges after the 0157 and 0158 PRs; contiguity holds once they land.
--
-- NULL means "no support text". The CHECK is PeerTube's own SUPPORT max (1000
-- characters, server/core/initializers/constants.ts) so an import can never
-- carry a value the API would refuse to round-trip. The video column is a
-- snapshot taken at create time (a new video inherits its channel's text), not
-- a live pointer: later editing the channel must not rewrite old videos.
ALTER TABLE channels ADD COLUMN support TEXT
    CONSTRAINT channels_support_len CHECK (support IS NULL OR char_length(support) <= 1000);
ALTER TABLE videos ADD COLUMN support TEXT
    CONSTRAINT videos_support_len CHECK (support IS NULL OR char_length(support) <= 1000);
