-- 0035: Federation (ActivityPub) actor keypairs for local accounts and channels.
-- Both users (Person) and channels (Group) are federated actors and each needs an
-- RSA keypair: the public key is served in the actor document; the private key
-- signs outbound activities. Kept in dedicated 1:1 tables (not columns on
-- users/channels) so the core account/channel models stay untouched — adding
-- columns to those tables would make several existing queries' RETURNING lists
-- diverge from the shared sqlc model and churn every caller. public_key_pem holds
-- the PEM SPKI public key; private_key_pem is a SECRET, stored as
-- "enc:<base64 nonce||ciphertext>" (AES-256-GCM under FEDERATION_KEY_KEK) or — only
-- in dev when no KEK is set — the raw PKCS#8 PEM. Rows are inserted lazily on first
-- federation use and cascade-deleted with their actor. See .ralph/specs/federation.md §2-3.
CREATE TABLE account_actor_keys (
    user_id         UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    public_key_pem  TEXT        NOT NULL,
    private_key_pem TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE channel_actor_keys (
    channel_id      UUID PRIMARY KEY REFERENCES channels (id) ON DELETE CASCADE,
    public_key_pem  TEXT        NOT NULL,
    private_key_pem TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
