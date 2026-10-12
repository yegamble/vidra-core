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
-- Rollback: releases before 0160 don't know the internal tier; the meta repo's
-- rollback guard refuses an app-only rollback to them while internal rows exist
-- (see deploy/rollback.sh). The down migration remaps internal → private.
--
-- Same drop/add pattern as 0074 (the inline column check is auto-named
-- videos_privacy_check).
ALTER TABLE videos DROP CONSTRAINT videos_privacy_check;
ALTER TABLE videos ADD CONSTRAINT videos_privacy_check
    CHECK (privacy IN ('public', 'unlisted', 'private', 'password', 'internal'));
