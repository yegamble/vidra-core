-- name: CreateStepUpToken :one
INSERT INTO step_up_tokens (user_id, session_id, provider, token_hash, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, user_id, session_id, provider, token_hash, expires_at, used_at, created_at;

-- name: ConsumeStepUpToken :one
-- Spend one step-up assertion, as ONE statement so it is atomic without a
-- transaction: used_at IS NULL is part of the predicate, so two concurrent
-- requests cannot both win, and the row is consumed by the same statement that
-- reads it. A token that is unknown, already spent, expired, issued to another
-- ACCOUNT, or issued to another SESSION matches nothing and the query returns
-- no rows — one indistinct answer for every invalid case, so a caller cannot
-- probe which.
UPDATE step_up_tokens
SET used_at = now()
WHERE token_hash = $1
  AND user_id = $2
  AND session_id = $3
  AND used_at IS NULL
  AND expires_at > now()
RETURNING id, provider;

-- name: DeleteUnusedStepUpTokensForSession :execrows
-- Supersede: a new challenge kills the session's previous unspent one, so at
-- most one step-up is ever live per session.
DELETE FROM step_up_tokens
WHERE session_id = $1 AND used_at IS NULL;

-- name: DeleteExpiredStepUpTokens :execrows
-- Opportunistic housekeeping, run at mint time. Spent and expired rows carry no
-- authority, so nothing is lost by dropping them, and doing it here keeps the
-- table bounded without adding a cron nobody would remember to deploy.
DELETE FROM step_up_tokens
WHERE expires_at < now();

-- name: MoveStepUpTokensToSession :execrows
-- Carry a live assertion across a refresh ROTATION.
--
-- A step-up can only complete as a top-level redirect back from the provider,
-- and a top-level navigation discards the in-memory access token — so the
-- landing page redeems a new one from the refresh cookie, and that rotation
-- revokes the session row the assertion was bound to and creates a new one.
-- Without this the binding is destroyed by the very page load that receives the
-- token, and the form it unlocks answers 403 with the row still sitting unspent.
--
-- It grants nothing new: a rotation requires the previous refresh token, which
-- only the browser that earned the assertion held. Spent and expired rows are
-- excluded so nothing dead is resurrected by moving it.
UPDATE step_up_tokens
SET session_id = sqlc.arg(to_session_id)
WHERE session_id = sqlc.arg(from_session_id)
  AND used_at IS NULL
  AND expires_at > now();
