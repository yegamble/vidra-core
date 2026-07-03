-- 0066: ATProto / Bluesky outbound cross-posting (Vidra extension P10.2, v1).
-- See .ralph/specs/atproto.md. This is gated by ATPROTO_ENABLED (default false)
-- and is independent of ActivityPub. v1 is OUTBOUND-ONLY: a creator links a
-- Bluesky account (handle + app password) and, when auto_post is on, Vidra
-- announces their newly published PUBLIC videos on Bluesky. No PDS hosting, no
-- inbound firehose, no DID login.

-- One linked Bluesky account per Vidra user. The app password is sealed at rest
-- with secretbox (AES-256-GCM) under ATPROTO_KEY_KEK (falling back to
-- FEDERATION_KEY_KEK); in dev with no KEK it is stored raw with a boot warning.
-- The password is NEVER returned by the API, logged, or exported.
CREATE TABLE atproto_accounts (
    user_id             UUID        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    handle              TEXT        NOT NULL,
    did                 TEXT        NOT NULL,
    pds_url             TEXT        NOT NULL DEFAULT 'https://bsky.social',
    app_password_sealed TEXT        NOT NULL,
    auto_post           BOOLEAN     NOT NULL DEFAULT false,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_posted_at      TIMESTAMPTZ
);

-- Durable outbound post queue (mirrors federation_deliveries states/backoff). On
-- the published transition of a PUBLIC video whose owner has auto_post, one row
-- is enqueued. A worker claims due rows, creates a fresh session on the owner's
-- PDS, and posts an app.bsky.feed.post record with an external-link embed to the
-- public watch URL; the resulting AT-URI is recorded in post_uri. Retries with
-- backoff, dead-letters (state 'failed') after a bounded number of attempts.
-- video_id is UNIQUE so a video is auto-posted at most once.
CREATE TABLE atproto_posts (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id        UUID        NOT NULL UNIQUE REFERENCES videos (id) ON DELETE CASCADE,
    state           TEXT        NOT NULL DEFAULT 'pending',
    attempts        INT         NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    error           TEXT        NOT NULL DEFAULT '',
    post_uri        TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Find due, still-pending posts cheaply.
CREATE INDEX atproto_posts_due_idx
    ON atproto_posts (next_attempt_at)
    WHERE state = 'pending';
