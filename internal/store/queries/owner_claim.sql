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

-- name: TransferInstanceOwner :one
-- Move the `is_owner` marker (0131) from whoever holds it to new_owner_id, in
-- ONE statement. Until this existed the marker had exactly one writer — the
-- claim CTE above — so an owner who deleted their own account left the instance
-- permanently unmarked, with `vidra doctor` telling the operator to write an
-- UPDATE by hand.
--
-- Why one statement rather than "clear, then set": `users_single_owner_idx` is a
-- partial UNIQUE index and is NOT deferrable, so the two writes cannot be split
-- without either a window in which the instance has no owner (a crash between
-- them leaves it unmarked, the exact state this route exists to escape) or a
-- window in which two rows claim the marker (which the index refuses outright).
--
-- Why the clear provably runs FIRST: `demoted` aggregates `cleared`, and an
-- aggregate must consume its whole input before it yields a row, so the main
-- UPDATE cannot begin until the old marker is gone. Without that forced
-- ordering PostgreSQL is free to run an unreferenced data-modifying CTE after
-- the main query, and every transfer would raise a unique violation.
--
-- Concurrency: two simultaneous transfers to DIFFERENT admins cannot both win.
-- The loser's `cleared` UPDATE blocks on the winner's row lock, re-evaluates
-- under READ COMMITTED against the committed row (which is no longer is_owner),
-- clears nothing, and its own set raises 23505 against the winner's row. The
-- caller maps that violation to a conflict rather than pretending it succeeded.
--
-- Eligibility is re-asserted here, not just in the service: the target must be a
-- live, non-tombstoned admin at write time, so a target demoted or deleted
-- between the service's read and this write yields no row instead of handing the
-- instance to an account that can no longer sign in to it. No row returned means
-- "the target was not eligible" — nothing was cleared either, because the clear
-- carries the same test.
WITH cleared AS (
    UPDATE users
    SET is_owner = FALSE, updated_at = now()
    WHERE is_owner
      AND id <> sqlc.arg('new_owner_id')
      AND EXISTS (
          SELECT 1 FROM users t
          WHERE t.id = sqlc.arg('new_owner_id')
            AND t.role = 'admin' AND t.is_active AND t.deleted_at IS NULL
      )
    RETURNING id
),
demoted AS (
    SELECT count(*)::bigint AS n FROM cleared
)
UPDATE users u
SET is_owner = TRUE, updated_at = now()
FROM demoted d
WHERE u.id = sqlc.arg('new_owner_id')
  AND u.role = 'admin' AND u.is_active AND u.deleted_at IS NULL
RETURNING u.id, u.username, u.email, d.n AS previous_owners_cleared;
