-- 0154: why and by whom an account was banned. users.is_active stays THE ban
-- flag (every session lookup already reads it); this row only explains it, so
-- the moderator who banned someone, the date and the stated reason survive in
-- the console and in the notice the user is sent (DSA statement of reasons).
-- A separate table rather than users columns: users has ~15 hand-listed
-- RETURNING clauses, and widening them all for three nullable columns would
-- touch every account query. Unban deletes the row.
CREATE TABLE user_bans (
    user_id   UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    reason    TEXT NOT NULL CHECK (char_length(reason) <= 500),
    banned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    banned_by UUID REFERENCES users(id) ON DELETE SET NULL
);
