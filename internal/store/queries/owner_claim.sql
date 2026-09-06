-- name: UpsertOwnerClaimToken :one
-- Mint (or re-mint) the single owner-claim token row (0104). Only the SHA-256
-- hash of the token is stored; upserting replaces any previous hash and clears
-- claimed_at, so exactly one token — the newest — is ever redeemable. Called
-- only while the users table is empty (boot bootstrap).
INSERT INTO owner_claim_tokens (id, token_hash)
VALUES (TRUE, $1)
ON CONFLICT (id) DO UPDATE
SET token_hash = EXCLUDED.token_hash, created_at = now(), claimed_at = NULL
RETURNING id, token_hash, created_at, claimed_at;

-- name: GetUnclaimedOwnerClaimToken :one
-- The live (unclaimed) claim token, if any. The service compares the presented
-- token's hash against token_hash in constant time — never in SQL — so this is
-- a plain fetch, not a lookup keyed by attacker-controlled input.
SELECT id, token_hash, created_at, claimed_at
FROM owner_claim_tokens
WHERE claimed_at IS NULL;

-- name: ClaimOwnerAndCreateAdmin :one
-- Redeem the owner-claim token and create THE admin account in one atomic
-- statement (the ApproveRegistrationRequest all-or-nothing CTE pattern in
-- registration_requests.sql). The `claimed_at IS NULL` guard is the DB-level
-- single-winner gate: of two concurrent claims, one UPDATE wins the row lock
-- and the loser's `claimed` CTE is empty, so it inserts nothing and returns no
-- row (the service maps that to an invalid-claim error). A unique violation on
-- the users insert rolls the whole statement back, leaving the token unclaimed.
WITH claimed AS (
    UPDATE owner_claim_tokens
    SET claimed_at = now()
    WHERE token_hash = sqlc.arg('token_hash') AND claimed_at IS NULL
    RETURNING id
),
ins AS (
    -- is_owner TRUE (0131): this statement is the ONLY place the marker is
    -- written, so the instance owner is exactly "the account the claim token
    -- created" and nothing else can mint one.
    INSERT INTO users (username, email, password_hash, role, history_enabled, is_owner)
    SELECT sqlc.arg('username'), sqlc.arg('email'), sqlc.arg('password_hash'),
           'admin', sqlc.arg('history_enabled')::bool, TRUE
    FROM claimed
    RETURNING id, username, email, password_hash, role, email_verified, is_active,
              created_at, updated_at, display_name, bio, pending_email_verification,
              history_enabled, search_history_enabled, personalized_search_enabled,
              personalized_recommendations_enabled
)
-- The three search/discovery columns are returned for the same reason the row is
-- returned at all: the claim response IS the owner's first session payload, and
-- the account page draws its toggles from it. Defaulted columns left out here
-- reach the client as Go zero values, i.e. as three controls the operator never
-- turned off.
SELECT ins.id, ins.username, ins.email, ins.password_hash, ins.role,
       ins.email_verified, ins.is_active, ins.created_at, ins.updated_at,
       ins.display_name, ins.bio, ins.pending_email_verification, ins.history_enabled,
       ins.search_history_enabled, ins.personalized_search_enabled,
       ins.personalized_recommendations_enabled
FROM ins;
