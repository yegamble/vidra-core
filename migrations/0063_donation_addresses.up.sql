-- 0063: Simple crypto donation addresses (fix_plan P14). NON-CUSTODIAL and
-- display-only by design: Vidra NEVER holds funds, balances, or private keys,
-- and processes no payments, payouts, escrow, or taxes. A creator lists public
-- wallet addresses on their account (profile) or on a specific channel they own;
-- viewers see the addresses (and whether ownership was cryptographically proven)
-- and send funds peer-to-peer, entirely outside Vidra. There is deliberately no
-- balance, transaction, invoice, or settlement column anywhere here.
CREATE TABLE donation_addresses (
    id                      UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    owner_id                UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- NULL channel_id = an account-level address (shown on the owner's profile);
    -- a set channel_id scopes the address to one channel the owner controls.
    channel_id              UUID        REFERENCES channels (id) ON DELETE CASCADE,
    -- Curated, closed set of supported networks. Each has a distinct address
    -- shape the application validates before insert; this CHECK is a defensive
    -- backstop so an unknown network can never reach the table.
    network                 TEXT        NOT NULL CHECK (network IN ('bitcoin', 'ethereum', 'litecoin', 'monero')),
    -- Bounded public address string (base58/bech32/hex depending on network).
    address                 TEXT        NOT NULL CHECK (char_length(address) BETWEEN 1 AND 128),
    -- Optional creator-facing label ("Main wallet", "Tips", ...).
    label                   TEXT        NOT NULL DEFAULT '' CHECK (char_length(label) <= 60),
    -- TRUE once the owner proved control by signing a challenge (see below).
    verified                BOOLEAN     NOT NULL DEFAULT FALSE,
    -- Signed-challenge bookkeeping. verification_nonce is a PUBLIC random
    -- challenge (never a key/secret) the owner embeds in a signed message;
    -- verification_expires_at bounds its validity (10 min). Both are cleared
    -- once verification succeeds. No private key or secret is ever stored here.
    verification_nonce      TEXT,
    verification_expires_at TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One address per (owner, channel-scope, network, address). A NULL channel_id
-- would compare as DISTINCT under a plain UNIQUE constraint, so account-level
-- rows are de-duplicated via COALESCE to a fixed sentinel UUID.
CREATE UNIQUE INDEX donation_addresses_owner_scope_idx
    ON donation_addresses (
        owner_id,
        COALESCE(channel_id, '00000000-0000-0000-0000-000000000000'::uuid),
        network,
        address
    );

-- Public reads select by owner (profile, account-level) or by channel.
CREATE INDEX donation_addresses_owner_idx ON donation_addresses (owner_id);
CREATE INDEX donation_addresses_channel_idx ON donation_addresses (channel_id) WHERE channel_id IS NOT NULL;
