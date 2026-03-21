-- +goose Up
CREATE TABLE IF NOT EXISTS abuse_report_messages (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    abuse_report_id UUID NOT NULL REFERENCES abuse_reports(id) ON DELETE CASCADE,
    sender_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message       TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_abuse_report_messages_report_id ON abuse_report_messages(abuse_report_id);

-- +goose Down
DROP TABLE IF EXISTS abuse_report_messages;
