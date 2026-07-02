-- 0036: Cached remote ActivityPub actors. When we receive a signed inbound
-- request (or follow a remote actor), we fetch its actor document once — through
-- the SSRF guard — and cache the fields we need: its inbox / shared inbox for
-- delivery, and its public key PEM for HTTP-signature verification. Keyed by the
-- canonical actor URL. See .ralph/specs/federation.md §2,7.
CREATE TABLE remote_actors (
    actor_url          TEXT PRIMARY KEY,
    actor_type         TEXT        NOT NULL,
    preferred_username TEXT        NOT NULL DEFAULT '',
    domain             TEXT        NOT NULL DEFAULT '',
    inbox_url          TEXT        NOT NULL DEFAULT '',
    shared_inbox_url   TEXT,
    public_key_pem     TEXT        NOT NULL,
    followers_url      TEXT        NOT NULL DEFAULT '',
    fetched_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
