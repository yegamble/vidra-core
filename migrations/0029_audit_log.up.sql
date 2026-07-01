-- 0029: Durable audit log for security-sensitive actions (auth, moderation,
-- admin, registration). Append-only and immutable: actor_id is stored WITHOUT a
-- foreign key so the trail survives account deletion. Rows never contain
-- secrets/PII — only a stable action id, result, the actor's id, a safe reason,
-- and the request id. Mirrors observability.AuditEvent.
CREATE TABLE audit_log (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    action      TEXT NOT NULL,
    result      TEXT NOT NULL,
    actor_id    UUID,
    reason      TEXT NOT NULL DEFAULT '',
    request_id  TEXT NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The admin audit view: newest first, optionally filtered by action.
CREATE INDEX audit_log_occurred_idx ON audit_log (occurred_at DESC);
CREATE INDEX audit_log_action_idx ON audit_log (action, occurred_at DESC);
