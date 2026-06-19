-- 0002: Minimal users and sessions foundation.
--
-- This is the auth substrate the rest of the platform builds on. It is kept
-- deliberately small; richer profile, channel, and account fields land in
-- later migrations as those features (PT-AUTH-ACCOUNT-SETUP) are implemented.

CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    username        TEXT NOT NULL,
    email           TEXT NOT NULL,
    password_hash   TEXT NOT NULL,
    role            TEXT NOT NULL DEFAULT 'user'
                        CHECK (role IN ('user', 'moderator', 'admin')),
    email_verified  BOOLEAN NOT NULL DEFAULT FALSE,
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Case-insensitive uniqueness for login identifiers.
CREATE UNIQUE INDEX users_username_lower_idx ON users (lower(username));
CREATE UNIQUE INDEX users_email_lower_idx ON users (lower(email));

-- Trigram index to support fuzzy account search.
CREATE INDEX users_username_trgm_idx ON users USING gin (username gin_trgm_ops);

-- Sessions back refresh-token rotation. The hashed token (never the raw token)
-- is stored; rotation replaces the row and revokes the prior one.
CREATE TABLE sessions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    refresh_hash    TEXT NOT NULL,
    user_agent      TEXT NOT NULL DEFAULT '',
    ip_address      INET,
    revoked_at      TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE UNIQUE INDEX sessions_refresh_hash_idx ON sessions (refresh_hash);
