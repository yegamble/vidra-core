-- 0080: server-side draft recovery for resumable uploads (backport W2.C2,
-- UPLOAD-02/03).
--
-- file_fingerprint — an OPAQUE, client-supplied identity for the file being
-- uploaded, so an in-progress session can be found again after the browser lost
-- its localStorage (a refresh, or a different device). The recommended recipe is
-- a SHA-256 over the file size concatenated with its first and last 1 MiB — cheap
-- to compute, stable across sessions, and collision-resistant enough to match
-- "the same file" without reading the whole thing. The server treats it as an
-- opaque string (<=128 chars, enforced at the HTTP boundary); it is never parsed,
-- only compared. Empty means the client supplied none (the pre-W2.C2 behaviour;
-- every existing row backfills to '').
ALTER TABLE upload_sessions
    ADD COLUMN file_fingerprint TEXT NOT NULL DEFAULT '';

-- Resume-by-fingerprint lookup for GET /api/v1/me/uploads?fingerprint=… : only
-- ACTIVE sessions with a non-empty fingerprint are resumable, so the index is
-- partial — it stays small and never indexes the '' default that most rows carry.
CREATE INDEX upload_sessions_user_fingerprint_idx
    ON upload_sessions (user_id, file_fingerprint)
    WHERE state = 'active' AND file_fingerprint <> '';
