-- 0102: surface a user's linked Bluesky/ATProto SIGN-IN handle on their public
-- profile, behind a per-user opt-in that defaults to hidden.
--
--   * oauth_identities.handle — the human-readable ATProto handle (e.g.
--     alice.bsky.social) resolved at link time. Nullable: remote handles are
--     mutable, OIDC identities carry none, and existing rows predate capture.
--     The DID (subject) stays the stable identity key; handle is display-only.
--   * users.show_bluesky — the opt-in. FALSE by default so a linked handle stays
--     private until the account explicitly chooses to display it.
ALTER TABLE oauth_identities ADD COLUMN handle text;
ALTER TABLE users ADD COLUMN show_bluesky boolean NOT NULL DEFAULT false;
