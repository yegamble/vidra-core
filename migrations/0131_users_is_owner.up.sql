-- 0131: Mark the instance owner (A16 admin guards).
--
-- WHY A COLUMN ON users, AND NOT AN INSTANCE SETTING.
-- The owner is not a fourth role — 0104 made the owner "the first admin,
-- claimed with the setup token", and it holds `admin` like any other admin.
-- What was missing is a durable marker so the guards can tell the instance
-- owner from the admin the owner later promoted. Three reasons this is a users
-- column rather than an id parked in instance_settings:
--
--   1. The guards already read the target's users row (GetUserByID / ListUsers),
--      so the marker costs no extra query and cannot go stale between reads.
--   2. `users_single_owner_idx` below makes "at most one owner" a DATABASE
--      invariant. A settings string is just text: nothing stops it naming two
--      accounts over time, an account that no longer exists, or a typo.
--   3. instance_settings is the operator-editable overlay, and every admin can
--      PATCH it — which is exactly the principal these guards defend against.
--      Ownership must not be writable by the same key that ownership restrains.
--
-- The marker is set from here on by the owner-claim path (ClaimOwnerAndCreateAdmin).
-- Transferring ownership is deliberately NOT implemented in this migration or
-- this slice: there is no route that writes this column after the claim.
ALTER TABLE users ADD COLUMN is_owner BOOLEAN NOT NULL DEFAULT FALSE;

-- At most one owner, enforced by the database. Partial so the FALSE rows (every
-- other account) are not indexed and never collide.
CREATE UNIQUE INDEX users_single_owner_idx ON users (is_owner) WHERE is_owner;

-- BACKFILL for instances that were claimed before this column existed.
--
-- Two INDEPENDENT sources, both exact rather than heuristic; a row is marked
-- only when they resolve to exactly one live account.
--
--   (a) audit_log: handleClaimOwner writes `auth.owner_claim` / success with the
--       CREATED ACCOUNT's id as actor_id (setup.go). When that row survives it
--       names the owner outright.
--   (b) owner_claim_tokens.claimed_at = users.created_at: the claim is ONE
--       statement (ClaimOwnerAndCreateAdmin) whose UPDATE sets claimed_at =
--       now() while the INSERT takes users.created_at from its `DEFAULT now()`.
--       Inside one statement now() is the transaction timestamp, so the two
--       timestamps are byte-identical BY CONSTRUCTION — this is an equality,
--       not a "within N seconds" window.
--
-- What the backfill CANNOT determine, and therefore leaves unmarked:
--   * an instance upgraded from before 0104 — it never minted a claim row and
--     never wrote an owner_claim audit event, so nothing anywhere records which
--     of its admins was first;
--   * an instance whose audit row has been pruned AND whose claim row was
--     re-minted (the upsert clears claimed_at) — neither source survives;
--   * an instance whose owner hard-deleted their account: the tombstone is
--     excluded on purpose (deleted_at IS NULL below), because it can never
--     authenticate and marking it would spend the single-owner slot on a row
--     that will never act;
--   * any case where the two sources point at different accounts, or either
--     matches more than one row — ambiguity is left unresolved rather than
--     guessed at.
-- An instance left without an owner marker keeps exactly today's behaviour:
-- every admin is equal, and only the last-admin guard applies. `vidra doctor`
-- reports the gap.
WITH claimed_via_audit AS (
    SELECT DISTINCT a.actor_id AS id
    FROM audit_log a
    WHERE a.action = 'auth.owner_claim' AND a.result = 'success' AND a.actor_id IS NOT NULL
),
claimed_via_timestamp AS (
    SELECT u.id
    FROM users u
    JOIN owner_claim_tokens t ON t.claimed_at = u.created_at
    WHERE t.claimed_at IS NOT NULL
),
candidates AS (
    SELECT id FROM claimed_via_audit
    UNION
    SELECT id FROM claimed_via_timestamp
),
-- Only a live, existing account may be marked, and only when the union above
-- collapses to exactly one of them.
resolved AS (
    SELECT u.id
    FROM users u
    WHERE u.id IN (SELECT id FROM candidates)
      AND u.deleted_at IS NULL
)
UPDATE users
SET is_owner = TRUE
WHERE id = (SELECT id FROM resolved)
  AND (SELECT count(*) FROM resolved) = 1;
