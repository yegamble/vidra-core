-- 0058: E2EE messaging (fix_plan P11.2, .ralph/specs/e2ee.md §3).
--
-- vidra-core is a dumb key-directory + ciphertext store: ALL cryptography is
-- client-side (Olm). The server never sees plaintext or private keys for
-- encrypted conversations — every column below carries either public key
-- material or opaque ciphertext.

-- A conversation is either plaintext OR E2EE, chosen at creation, immutable
-- (no update path exists). Encrypted 1:1 conversations use a distinct
-- "e2ee:<uuid>:<uuid>" dm_key namespace so a pair of users can have both a
-- plaintext and an encrypted thread.
ALTER TABLE conversations
    ADD COLUMN encrypted BOOLEAN NOT NULL DEFAULT FALSE;

-- A user's registered E2EE devices. identity_key (Curve25519) and signing_key
-- (Ed25519) are PUBLIC keys, uploaded by the client; the private halves never
-- leave the device.
CREATE TABLE e2ee_devices (
    id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id      UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    device_name  TEXT        NOT NULL,
    identity_key TEXT        NOT NULL,
    signing_key  TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX e2ee_devices_user_idx ON e2ee_devices (user_id);

-- One-time prekeys (public), uploaded in batches by the owning device and
-- claimed exactly once by a peer establishing an Olm session. Claiming is
-- atomic: UPDATE … SET claimed_at WHERE claimed_at IS NULL … FOR UPDATE
-- SKIP LOCKED, so concurrent claimers can never receive the same key.
CREATE TABLE e2ee_one_time_keys (
    id         UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    device_id  UUID        NOT NULL REFERENCES e2ee_devices (id) ON DELETE CASCADE,
    key_id     TEXT        NOT NULL,
    key        TEXT        NOT NULL,
    claimed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Re-uploading the same key_id is a no-op (idempotent replenishment).
    UNIQUE (device_id, key_id)
);
-- The claim scan and the unclaimed count both hit this partial index.
CREATE INDEX e2ee_one_time_keys_unclaimed_idx
    ON e2ee_one_time_keys (device_id, created_at, id)
    WHERE claimed_at IS NULL;

-- Encrypted envelopes: one row per RECIPIENT DEVICE (the sending client fans
-- out, encrypting separately per device pair). ciphertext is opaque, bounded
-- (64 KiB) Olm output; message_type is Olm prekey=0 / normal=1. Deleting a
-- device cascades both directions: envelopes TO it are undecryptable by anyone
-- else, and envelopes FROM it are dropped with it (documented v1 behavior).
-- expires_at drives disappearing messages: reads filter it, and a sweeper
-- hard-deletes expired rows.
CREATE TABLE e2ee_messages (
    id                  UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    conversation_id     UUID        NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    sender_device_id    UUID        NOT NULL REFERENCES e2ee_devices (id) ON DELETE CASCADE,
    recipient_device_id UUID        NOT NULL REFERENCES e2ee_devices (id) ON DELETE CASCADE,
    message_type        INT         NOT NULL CHECK (message_type IN (0, 1)),
    ciphertext          TEXT        NOT NULL CHECK (length(ciphertext) <= 65536),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ
);
-- Reads: envelopes addressed to one of the caller's devices, newest first.
CREATE INDEX e2ee_messages_recipient_idx
    ON e2ee_messages (conversation_id, recipient_device_id, created_at DESC);
-- The disappearing-message sweeper scans rows past their expires_at.
CREATE INDEX e2ee_messages_expiry_idx
    ON e2ee_messages (expires_at)
    WHERE expires_at IS NOT NULL;
