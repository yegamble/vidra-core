-- +goose Up
-- +goose StatementBegin
-- Phase 11 — per-channel ATProto cross-post default ('always' / 'never' / 'ask').
-- 'ask' is the default: opt-in distribution. Channel owner can flip in settings.
ALTER TABLE channels
ADD COLUMN IF NOT EXISTS atproto_cross_post_mode TEXT NOT NULL DEFAULT 'ask'
    CHECK (atproto_cross_post_mode IN ('always', 'never', 'ask'));

COMMENT ON COLUMN channels.atproto_cross_post_mode IS 'Default cross-post behavior for uploads on this channel: always (auto-post), never (disabled), ask (per-upload toggle).';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE channels DROP COLUMN IF EXISTS atproto_cross_post_mode;
-- +goose StatementEnd
