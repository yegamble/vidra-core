-- 0028: Registration approval queue. When REGISTRATION_REQUIRE_APPROVAL is on, a
-- signup does not create an account directly; it files a pending registration
-- request (holding the bcrypt password hash — never plaintext). Admins approve
-- (which creates the account from the stored hash) or reject. The partial unique
-- indexes allow at most one PENDING request per email/username (case-insensitive)
-- while still permitting a fresh request after a rejection.
CREATE TABLE registration_requests (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    username       TEXT NOT NULL,
    email          TEXT NOT NULL,
    password_hash  TEXT NOT NULL,
    note           TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    moderator_note TEXT NOT NULL DEFAULT '',
    reviewed_by    UUID REFERENCES users (id) ON DELETE SET NULL,
    reviewed_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Queue: newest requests first, filterable by status.
CREATE INDEX registration_requests_status_created_idx ON registration_requests (status, created_at DESC);
-- At most one PENDING request per email / username (case-insensitive).
CREATE UNIQUE INDEX registration_requests_pending_email_idx
    ON registration_requests (lower(email)) WHERE status = 'pending';
CREATE UNIQUE INDEX registration_requests_pending_username_idx
    ON registration_requests (lower(username)) WHERE status = 'pending';
