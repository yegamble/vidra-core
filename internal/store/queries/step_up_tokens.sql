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
