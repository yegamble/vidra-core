-- Why a session row was revoked, so refresh-token REUSE detection can tell a
-- replayed rotated token (the compromise signal — revoke everything) from a
-- client that was DELIBERATELY signed out and simply retried (refuse, and leave
-- the other sessions alone).
--
-- Without it the two are indistinguishable: a password change signs the other
-- devices out, each of their clients auto-refreshes on its first 401, and the
-- escalation takes down the very session the change was supposed to keep.
--
-- '' is the pre-existing, unclassified state: rows revoked before this
-- migration keep escalating exactly as they did, which is the safe direction.
ALTER TABLE sessions
    ADD COLUMN revoked_reason TEXT NOT NULL DEFAULT '';
