-- 0031: Normal (non-E2EE) direct messaging. A conversation groups participants
-- and messages. This slice covers 1:1 DMs: `dm_key` is the canonical, sorted
-- "<lower-uuid>:<higher-uuid>" of the two participants, uniquely identifying a
-- 1:1 conversation so start-or-get is idempotent. It is NULL for future group
-- conversations. Message bodies are stored in plaintext (E2EE is P11.2 — a later
-- slice storing ciphertext only).
CREATE TABLE conversations (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    dm_key     TEXT UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Bumped when a message is sent, so conversation lists sort by recency.
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE conversation_participants (
    conversation_id UUID NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX conversation_participants_user_idx ON conversation_participants (user_id);

CREATE TABLE messages (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    conversation_id UUID NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    sender_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    body            TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX messages_conversation_created_idx ON messages (conversation_id, created_at DESC);
