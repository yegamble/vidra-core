-- name: CreateRegistrationRequest :one
-- File a pending registration request. A duplicate PENDING request for the same
-- email/username raises a unique violation (23505), which the service maps to a
-- conflict.
INSERT INTO registration_requests (username, email, password_hash, note)
VALUES ($1, $2, $3, $4)
RETURNING id, username, email, note, status, moderator_note, reviewed_at, created_at;

-- name: ListRegistrationRequests :many
-- The approval queue, newest first, with the reviewing admin's username resolved.
-- status is the exact lifecycle state to show, or NULL for all of them. It used
-- to be a pending_only BOOLEAN fed by `?status == "pending"` in the handler,
-- which meant ?status=approved silently returned the whole queue; the filter is
-- now the value itself so an unknown one is rejected up front instead of
-- collapsing into "no filter". The password hash is never selected.
SELECT r.id, r.username, r.email, r.note, r.status, r.moderator_note,
       r.reviewed_at, r.created_at,
       u.username AS reviewer_username
FROM registration_requests r
LEFT JOIN users u ON u.id = r.reviewed_by
WHERE (sqlc.narg('status')::text IS NULL OR r.status = sqlc.narg('status')::text)
ORDER BY r.created_at DESC, r.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountRegistrationRequests :one
-- How many rows ListRegistrationRequests would return for the same status
-- filter, ignoring pagination. The WHERE must stay identical to it.
SELECT count(*)::bigint
FROM registration_requests r
WHERE (sqlc.narg('status')::text IS NULL OR r.status = sqlc.narg('status')::text);

-- name: ApproveRegistrationRequest :one
-- Atomically approve a PENDING request: create the user account from the stored
-- hash and mark the request approved, in a single statement (data-modifying CTEs
-- run exactly once, so this is all-or-nothing). Returns the created user. If the
-- request is not pending, `req` is empty so nothing is inserted/updated and no
-- row is returned (the service maps that to not-found). If the username/email is
-- now taken, the users insert raises a unique violation and the whole statement
-- rolls back, leaving the request pending.
WITH req AS (
    SELECT registration_requests.id, registration_requests.username,
           registration_requests.email, registration_requests.password_hash
    FROM registration_requests
    WHERE registration_requests.id = sqlc.arg('id') AND registration_requests.status = 'pending'
),
ins AS (
    INSERT INTO users (username, email, password_hash, role, pending_email_verification, history_enabled)
    SELECT req.username, req.email, req.password_hash, 'user',
           sqlc.arg('pending_email_verification')::bool, sqlc.arg('history_enabled')::bool
    FROM req
    RETURNING id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, pending_email_verification
),
upd AS (
    UPDATE registration_requests
    SET status = 'approved', reviewed_by = sqlc.arg('reviewed_by'), reviewed_at = now(), updated_at = now()
    WHERE registration_requests.id = (SELECT req.id FROM req)
    RETURNING registration_requests.id
)
SELECT ins.id, ins.username, ins.email, ins.password_hash, ins.role, ins.email_verified,
       ins.is_active, ins.created_at, ins.updated_at, ins.display_name, ins.bio,
       ins.pending_email_verification
FROM ins;

-- name: RejectRegistrationRequest :one
-- Reject a PENDING request with a moderator note. Returns the applicant's
-- username and address (no rows = unknown/already-resolved id, which the caller
-- maps to 404): the rejection notice has to reach the person who applied, and
-- reading the row separately would be a second query racing this one — a
-- concurrent reject could otherwise mail the same applicant twice.
UPDATE registration_requests
SET status = 'rejected', moderator_note = sqlc.arg('moderator_note'),
    reviewed_by = sqlc.arg('reviewed_by'), reviewed_at = now(), updated_at = now()
WHERE id = sqlc.arg('id') AND status = 'pending'
RETURNING username, email;
