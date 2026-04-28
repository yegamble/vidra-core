-- +goose Up
-- +goose StatementBegin

-- Phase 9 Inner Circle — core tables.
-- Three tables:
--   inner_circle_tiers          per-channel price + perks for fixed tier IDs
--   inner_circle_memberships    user × channel × tier rows with status + expires_at
--   channel_posts               text-only posts on a channel's Members tab (Phase 9 v1)
--   polar_webhook_events        event_id dedupe for Polar webhook ledger writes
-- Tier IDs are fixed: 'supporter','vip','elite'. Membership is at most one active per (user, channel).
-- Image attachments and threaded post-comments are deferred to Phase 9b.

-- ─── inner_circle_tiers ─────────────────────────────────────────────────────
CREATE TABLE inner_circle_tiers (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    tier_id VARCHAR(16) NOT NULL CHECK (tier_id IN ('supporter','vip','elite')),
    monthly_usd_cents INTEGER NOT NULL DEFAULT 299 CHECK (monthly_usd_cents >= 0),
    monthly_sats BIGINT NOT NULL DEFAULT 8500 CHECK (monthly_sats >= 0),
    perks TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (channel_id, tier_id)
);

CREATE INDEX idx_inner_circle_tiers_channel ON inner_circle_tiers(channel_id);

CREATE TRIGGER update_inner_circle_tiers_updated_at
    BEFORE UPDATE ON inner_circle_tiers
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- ─── inner_circle_memberships ───────────────────────────────────────────────
CREATE TABLE inner_circle_memberships (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    tier_id VARCHAR(16) NOT NULL CHECK (tier_id IN ('supporter','vip','elite')),
    status VARCHAR(16) NOT NULL CHECK (status IN ('active','pending','cancelled','expired')),
    started_at TIMESTAMPTZ NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    polar_subscription_id VARCHAR(255) NULL,
    btcpay_invoice_id UUID NULL REFERENCES btcpay_invoices(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ic_memberships_user ON inner_circle_memberships(user_id);
CREATE INDEX idx_ic_memberships_channel ON inner_circle_memberships(channel_id);
CREATE INDEX idx_ic_memberships_status_expires ON inner_circle_memberships(status, expires_at);
CREATE INDEX idx_ic_memberships_user_channel_status_expires
    ON inner_circle_memberships(user_id, channel_id, status, expires_at);

-- Polar subscription IDs are unique when set (NULL allowed for BTCPay-only memberships).
CREATE UNIQUE INDEX uniq_ic_memberships_polar_sub
    ON inner_circle_memberships(polar_subscription_id)
    WHERE polar_subscription_id IS NOT NULL;

-- At most one active OR pending row per (user, channel). Cancelled / expired rows accumulate as history.
CREATE UNIQUE INDEX uniq_ic_memberships_active_or_pending
    ON inner_circle_memberships(user_id, channel_id)
    WHERE status IN ('active','pending');

CREATE TRIGGER update_inner_circle_memberships_updated_at
    BEFORE UPDATE ON inner_circle_memberships
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- ─── channel_posts (text-only in Phase 9; no attachments column) ────────────
CREATE TABLE channel_posts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 4096),
    tier_id VARCHAR(16) NULL CHECK (tier_id IS NULL OR tier_id IN ('supporter','vip','elite')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_channel_posts_channel_created ON channel_posts(channel_id, created_at DESC);

CREATE TRIGGER update_channel_posts_updated_at
    BEFORE UPDATE ON channel_posts
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- ─── polar_webhook_events ───────────────────────────────────────────────────
-- Used for ledger-side idempotency only. Membership state itself is keyed on
-- polar_subscription_id (UPSERT semantics in T5), so this table only prevents
-- duplicate ledger entries on retried webhooks.
CREATE TABLE polar_webhook_events (
    event_id VARCHAR(255) PRIMARY KEY,
    event_type VARCHAR(64) NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_polar_webhook_events_processed_at ON polar_webhook_events(processed_at DESC);

-- ─── Seed default tiers for existing channels (idempotent) ──────────────────
INSERT INTO inner_circle_tiers (channel_id, tier_id, monthly_usd_cents, monthly_sats, perks)
SELECT c.id, 'supporter', 299, 8500,
       ARRAY['Supporter badge in comments','Access to members-only posts','Name on supporter wall','Ad-free channel viewing']
FROM channels c
ON CONFLICT (channel_id, tier_id) DO NOTHING;

INSERT INTO inner_circle_tiers (channel_id, tier_id, monthly_usd_cents, monthly_sats, perks)
SELECT c.id, 'vip', 799, 22750,
       ARRAY['Everything in Supporter','Exclusive VIP badge','Early access to new videos','Members-only live chats','Behind-the-scenes content','Monthly Q&A sessions']
FROM channels c
ON CONFLICT (channel_id, tier_id) DO NOTHING;

INSERT INTO inner_circle_tiers (channel_id, tier_id, monthly_usd_cents, monthly_sats, perks)
SELECT c.id, 'elite', 1999, 56950,
       ARRAY['Everything in VIP','Shoutout in videos','Direct message access','Exclusive merch discounts','Credits in video descriptions','Priority feature requests','Annual meet & greet (virtual)']
FROM channels c
ON CONFLICT (channel_id, tier_id) DO NOTHING;

COMMENT ON TABLE inner_circle_tiers IS 'Per-channel Inner Circle tier pricing + perks. Tier IDs are fixed (supporter/vip/elite); only price/perks/enabled vary per channel.';
COMMENT ON TABLE inner_circle_memberships IS 'User memberships in a channel''s Inner Circle. Status: active|pending|cancelled|expired. Polar memberships have polar_subscription_id; BTCPay memberships have btcpay_invoice_id. Phase 9.';
COMMENT ON TABLE channel_posts IS 'Text-only posts on a channel''s Members tab. tier_id NULL = visible to anyone; non-NULL gates by membership tier. Phase 9 v1 — image attachments deferred to 9b.';
COMMENT ON TABLE polar_webhook_events IS 'Polar webhook event_id dedupe for ledger-side accounting. Membership state itself is keyed on polar_subscription_id.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS polar_webhook_events CASCADE;
DROP TABLE IF EXISTS channel_posts CASCADE;
DROP TABLE IF EXISTS inner_circle_memberships CASCADE;
DROP TABLE IF EXISTS inner_circle_tiers CASCADE;

-- +goose StatementEnd
