-- 0160: the 'internal' video privacy tier (PeerTube parity, VideoPrivacy 4).
--
-- An internal video is visible to any signed-in, active account of THIS
-- instance and to nobody anonymous (internal/video/privacy.go). Until now the
-- PeerTube importer had nowhere to put one and mapped it to 'private', so a
-- migrated "members only" video vanished for every member but its owner.
--
-- Only the CHECK widens. Every discovery, federation, search-reconcile, RSS,
-- sitemap, ATProto and public-IPFS query already allowlists privacy='public', so
-- an internal row is excluded from all of them without touching one.
--
-- ROLLBACK HAZARD (one-release compat): a release that predates this tier reads
-- privacy with `== 'private'` comparisons, so it would serve an internal video
-- by direct link to an ANONYMOUS viewer. Before an app-only rollback to such a
-- release, run the down migration's UPDATE first (internal → private: the
-- stricter tier, never the looser one).
--
-- Same drop/add pattern as 0074 (the inline column check is auto-named
-- videos_privacy_check).
ALTER TABLE videos DROP CONSTRAINT videos_privacy_check;
ALTER TABLE videos ADD CONSTRAINT videos_privacy_check
    CHECK (privacy IN ('public', 'unlisted', 'private', 'password', 'internal'));
