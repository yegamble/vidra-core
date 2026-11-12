-- name: CreateRegistrationRequest :one
-- File a pending registration request. A duplicate PENDING request for the same
-- email/username raises a unique violation (23505), which the service maps to a
-- conflict.
INSERT INTO registration_requests (
    username, email, password_hash, note,
    consent_accepted_at, consent_terms_url, consent_terms_version,
    consent_privacy_url, consent_privacy_version, consent_minimum_age
)
VALUES (
    sqlc.arg('username'), sqlc.arg('email'), sqlc.arg('password_hash'), sqlc.arg('note'),
    -- What the applicant accepted, and when: NOW, as they submit this — not
    -- whenever an admin gets to the queue. All NULL when nothing was accepted.
    CASE WHEN sqlc.arg('consent_recorded')::bool THEN now() END,
    sqlc.narg('consent_terms_url'), sqlc.narg('consent_terms_version'),
    sqlc.narg('consent_privacy_url'), sqlc.narg('consent_privacy_version'),
    sqlc.narg('consent_minimum_age')
)
RETURNING id, username, email, note, status, moderator_note, reviewed_at, created_at;

-- name: CreateProviderRegistrationRequest :one
-- File a pending registration request for a VERIFIED provider identity (OIDC or
-- ATProto). password_hash is '' by the same convention users.password_hash uses
-- for provider-created accounts — an empty hash bcrypt can never verify — so the
-- approved account's only credential is the identity attached at approval.
-- A duplicate PENDING request for the same identity, username or email raises a
-- unique violation (23505).
INSERT INTO registration_requests (
    username, email, password_hash, note,
    oauth_provider, oauth_subject, oauth_handle, oauth_email, oauth_email_verified,
    consent_accepted_at, consent_terms_url, consent_terms_version,
    consent_privacy_url, consent_privacy_version, consent_minimum_age
)
VALUES (
    sqlc.arg('username'), sqlc.arg('email'), '', '',
    sqlc.arg('oauth_provider'), sqlc.arg('oauth_subject'), sqlc.narg('oauth_handle'),
    sqlc.arg('oauth_email'), sqlc.arg('oauth_email_verified'),
    -- What the applicant accepted, and when: NOW, as they submit this — not
    -- whenever an admin gets to the queue. All NULL when nothing was accepted.
    CASE WHEN sqlc.arg('consent_recorded')::bool THEN now() END,
    sqlc.narg('consent_terms_url'), sqlc.narg('consent_terms_version'),
    sqlc.narg('consent_privacy_url'), sqlc.narg('consent_privacy_version'),
    sqlc.narg('consent_minimum_age')
)
RETURNING id, username, email, note, status, moderator_note, reviewed_at, created_at;

-- name: GetPendingProviderRegistrationRequest :one
-- The unresolved request for a provider identity, if any. Lets a repeat sign-in
-- answer "still pending" instead of filing a duplicate or, worse, reporting a
-- conflict the applicant cannot act on.
SELECT id, username, email, status, created_at
FROM registration_requests
WHERE oauth_provider = sqlc.arg('oauth_provider')
  AND oauth_subject = sqlc.arg('oauth_subject')
  AND status = 'pending';

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
           registration_requests.email, registration_requests.password_hash,
           registration_requests.oauth_provider, registration_requests.oauth_subject,
           registration_requests.oauth_handle, registration_requests.oauth_email,
           registration_requests.oauth_email_verified,
           registration_requests.consent_accepted_at,
           registration_requests.consent_terms_url, registration_requests.consent_terms_version,
           registration_requests.consent_privacy_url, registration_requests.consent_privacy_version,
           registration_requests.consent_minimum_age
    FROM registration_requests
    WHERE registration_requests.id = sqlc.arg('id') AND registration_requests.status = 'pending'
),
ins AS (
    INSERT INTO users (username, email, password_hash, role, pending_email_verification, history_enabled, email_verified)
    SELECT req.username, req.email, req.password_hash, 'user',
           -- A provider request is never held for email verification: the IdP
           -- attested the address, exactly as the direct provider create path
           -- reasons (internal/auth/oauth.go).
           sqlc.arg('pending_email_verification')::bool AND req.oauth_provider IS NULL,
           sqlc.arg('history_enabled')::bool,
           req.oauth_email_verified
    FROM req
    RETURNING id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, pending_email_verification
),
-- Attach the identity the applicant applied WITH, so the approved account signs
-- in through the same subject rather than merely one asserting the same address.
-- In the same statement as the user insert: an account with no credential is
-- exactly the state a second query could leave behind if it failed.
ident AS (
    INSERT INTO oauth_identities (provider, subject, user_id, email, handle)
    SELECT req.oauth_provider, req.oauth_subject, ins.id, req.oauth_email, req.oauth_handle
    FROM req, ins
    WHERE req.oauth_provider IS NOT NULL
    RETURNING oauth_identities.id
),
-- The account's consent record (0189), from what the request kept: the
-- documents the applicant accepted, at the time THEY accepted them. Same
-- statement as the account, as on every other path that creates one. A request
-- that recorded nothing (filed before 0189, or with no documents configured)
-- has a NULL consent_accepted_at and adds no row.
consents AS (
    INSERT INTO user_consents (user_id, document, version, url, minimum_age, source, accepted_at)
    SELECT ins.id, d.document, COALESCE(d.version, ''), COALESCE(d.url, ''), d.minimum_age,
           'approval', req.consent_accepted_at
    FROM req, ins,
         LATERAL (VALUES
             ('terms',   req.consent_terms_version,   req.consent_terms_url,   NULL::int, req.consent_terms_url IS NOT NULL),
             ('privacy', req.consent_privacy_version, req.consent_privacy_url, NULL::int, req.consent_privacy_url IS NOT NULL),
             ('age',     NULL::text,                  NULL::text,              req.consent_minimum_age, req.consent_minimum_age IS NOT NULL)
         ) AS d(document, version, url, minimum_age, accepted)
    WHERE req.consent_accepted_at IS NOT NULL AND d.accepted
    RETURNING user_consents.id
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
