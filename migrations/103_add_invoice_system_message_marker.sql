-- +goose Up
-- +goose StatementBegin
-- Phase 10 — Tip-mediated chat system message replay protection.
-- system_message_broadcast_at is set the first time an invoice is used to publish a system
-- message into a live-stream chat ("Alice tipped 1000 sat"). A second attempt with the same
-- invoice is rejected with 409 Conflict — prevents an attacker who tipped 1 sat once from
-- spamming the chat with N broadcasts.
ALTER TABLE btcpay_invoices
ADD COLUMN IF NOT EXISTS system_message_broadcast_at TIMESTAMPTZ NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE btcpay_invoices DROP COLUMN IF EXISTS system_message_broadcast_at;
-- +goose StatementEnd
