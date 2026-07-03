-- 0047: account-level "unlisted" flag (product-decisions.md §16). An unlisted
-- owner's videos and channels are excluded from public discovery surfaces
-- (feed, search) while direct channel/video URLs keep serving. Full per-field
-- profile privacy is intentionally out of scope.
ALTER TABLE users ADD COLUMN unlisted BOOLEAN NOT NULL DEFAULT FALSE;
