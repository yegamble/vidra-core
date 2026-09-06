-- name: CreateEmailChangeRequest :one
INSERT INTO email_change_requests (user_id, new_email, token_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING id, user_id, new_email, token_hash, expires_at, used_at, created_at;

-- name: GetPendingEmailChangeRequest :one
-- The readable PENDING state: the newest request for this account that is
-- neither used nor expired. Requests are superseded by deletion, so in practice
-- there is at most one; ORDER BY makes the read deterministic anyway.
SELECT id, user_id, new_email, token_hash, expires_at, used_at, created_at
FROM email_change_requests
WHERE user_id = $1 AND used_at IS NULL AND expires_at > now()
ORDER BY created_at DESC
LIMIT 1;

-- name: DeleteUnusedEmailChangeRequests :execrows
-- Supersede (a second request kills the first token) and cancel. :execrows so
-- cancel can tell "there was something pending" from "there was nothing",
-- rather than reporting success for a no-op.
DELETE FROM email_change_requests
WHERE user_id = $1 AND used_at IS NULL;

-- name: ConfirmEmailChange :one
-- The switch, as ONE statement, so it is atomic without a transaction: the CTE
-- consumes the token (used_at IS NULL is part of the predicate, so two
-- concurrent confirmations cannot both win) and the outer UPDATE moves the
-- address only for the row the token actually belongs to. A token that is
-- unknown, already used, expired, or owned by ANOTHER account matches nothing,
-- the CTE is empty, no user row is touched, and the query returns no rows —
-- one indistinct answer for every invalid case, so a caller cannot probe which.
--
-- email_verified is set TRUE in the same statement: consuming a token delivered
-- to the new address IS the possession proof, so an account can never end up
-- holding an address it has not verified.
WITH consumed AS (
    UPDATE email_change_requests
    SET used_at = now()
    WHERE token_hash = $1
      AND user_id = $2
      AND used_at IS NULL
      AND expires_at > now()
    RETURNING id, user_id, new_email
)
UPDATE users u
SET email          = consumed.new_email,
    email_verified = TRUE,
    updated_at     = now()
FROM consumed
WHERE u.id = consumed.user_id
RETURNING u.id, u.email;
