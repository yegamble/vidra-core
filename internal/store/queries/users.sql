-- name: GetUserByID :one
SELECT id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes, unlisted, bypass_quarantine, deleted_at, pending_email_verification, history_enabled, profile_public, search_history_enabled, personalized_search_enabled, personalized_recommendations_enabled, sensitive_content_policy, show_bluesky, is_owner
FROM users
WHERE id = $1;

-- name: GetPublicUserProfileByUsername :one
-- Private, inactive, and unknown accounts deliberately collapse to no rows so
-- the HTTP surface returns the same non-enumerating 404 for all three. The
-- LEFT JOIN surfaces the account's linked ATProto (Bluesky) sign-in handle;
-- show_bluesky gates whether the HTTP layer actually exposes bluesky_handle.
SELECT u.id, u.username, u.display_name, u.bio, u.created_at, u.profile_public,
       u.show_bluesky, oi.handle AS bluesky_handle
FROM users u
LEFT JOIN oauth_identities oi ON oi.user_id = u.id AND oi.provider = 'atproto'
WHERE lower(u.username) = lower($1)
  AND u.is_active = TRUE
  AND u.profile_public = TRUE;

-- name: GetUserActorByUsername :one
-- Minimal, secret-free account fields for the ActivityPub Person actor. Only
-- active accounts are federated (deactivated accounts 404).
SELECT id, username, display_name, bio, created_at
FROM users
WHERE lower(username) = lower($1) AND is_active = true;

-- name: GetUserActorByID :one
-- GetUserActorByUsername keyed by id — resolves the authenticated caller's
-- actor identity (username) for outbound account-actor activities.
SELECT id, username, display_name, bio, created_at
FROM users
WHERE id = $1 AND is_active = true;

-- name: GetUserByEmail :one
SELECT id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes, unlisted, bypass_quarantine, deleted_at, pending_email_verification, history_enabled, profile_public, search_history_enabled, personalized_search_enabled, personalized_recommendations_enabled, sensitive_content_policy, show_bluesky, is_owner
FROM users
WHERE lower(email) = lower($1);

-- name: GetUserByLoginIdentifier :one
-- Sign-in lookup for "email OR username" in ONE round trip, with deterministic
-- precedence: EMAIL ALWAYS WINS. Usernames carry no charset restriction
-- historically, so a username may look like — and may literally equal — another
-- account's email address (the unique indexes are per-column, never across
-- them). Ordering the email branch first makes the owner of the email address
-- the only account reachable by that string, so nobody can shadow another
-- account's sign-in by choosing a lookalike username.
--
-- Deliberately NO is_active filter (unlike GetUserByUsername): the disabled
-- check must stay AFTER the password compare in Go, otherwise a disabled
-- account answers differently before any credential is proven and becomes an
-- enumeration oracle.
--
-- Both branches are index-served (users_email_lower_idx / users_username_lower_idx).
SELECT id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes, unlisted, bypass_quarantine, deleted_at, pending_email_verification, history_enabled, profile_public, search_history_enabled, personalized_search_enabled, personalized_recommendations_enabled, sensitive_content_policy, show_bluesky, is_owner
FROM (
    SELECT id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes, unlisted, bypass_quarantine, deleted_at, pending_email_verification, history_enabled, profile_public, search_history_enabled, personalized_search_enabled, personalized_recommendations_enabled, sensitive_content_policy, show_bluesky, is_owner, 1 AS match_priority
    FROM users
    WHERE lower(email) = lower($1)
    UNION ALL
    SELECT id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes, unlisted, bypass_quarantine, deleted_at, pending_email_verification, history_enabled, profile_public, search_history_enabled, personalized_search_enabled, personalized_recommendations_enabled, sensitive_content_policy, show_bluesky, is_owner, 2 AS match_priority
    FROM users
    WHERE lower(username) = lower($1)
) AS matches
ORDER BY match_priority
LIMIT 1;

-- name: GetUserByUsername :one
-- Resolve a username to a full user row, case-insensitive and active-only
-- (deactivated accounts are treated as not found → the caller 404s, so an
-- inactive account's existence is not leaked differently from an unknown one).
-- Used to start a DM by username instead of by id.
SELECT id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes, unlisted, bypass_quarantine, deleted_at, pending_email_verification, history_enabled, profile_public, search_history_enabled, personalized_search_enabled, personalized_recommendations_enabled, sensitive_content_policy, show_bluesky, is_owner
FROM users
WHERE lower(username) = lower($1) AND is_active = true;

-- name: CreateUser :one
-- pending_email_verification is TRUE only when the account is created while
-- the registration email-verification gate is active (W7 — the grandfather
-- clause lives in this flag, never in a retroactive check); history_enabled
-- is seeded from the new_user_history_enabled instance setting.
INSERT INTO users (username, email, password_hash, role, pending_email_verification, history_enabled)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes, unlisted, bypass_quarantine, deleted_at, pending_email_verification, history_enabled, profile_public, search_history_enabled, personalized_search_enabled, personalized_recommendations_enabled, sensitive_content_policy, show_bluesky, is_owner;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: UpdateUserProfile :one
UPDATE users
SET display_name = COALESCE(sqlc.narg('display_name'), display_name),
    bio          = COALESCE(sqlc.narg('bio'), bio),
    unlisted     = COALESCE(sqlc.narg('unlisted'), unlisted),
    history_enabled = COALESCE(sqlc.narg('history_enabled'), history_enabled),
    profile_public = COALESCE(sqlc.narg('profile_public'), profile_public),
    -- Search & recommendation preferences (search-service W4): the user half of
    -- the two-factor personalization gate. Partial: NULL args leave each unchanged.
    search_history_enabled = COALESCE(sqlc.narg('search_history_enabled'), search_history_enabled),
    personalized_search_enabled = COALESCE(sqlc.narg('personalized_search_enabled'), personalized_search_enabled),
    personalized_recommendations_enabled = COALESCE(sqlc.narg('personalized_recommendations_enabled'), personalized_recommendations_enabled),
    -- Per-user opt-in to display the linked Bluesky/ATProto handle on the public
    -- profile (0102). Partial: a NULL arg leaves it unchanged. Default FALSE.
    show_bluesky = COALESCE(sqlc.narg('show_bluesky'), show_bluesky),
    -- Per-user sensitive-content policy override (0100). Tri-state: unchanged
    -- unless set_sensitive_content_policy is true, in which case a NULL value
    -- clears the override (inherit the instance policy) and a non-NULL enum value
    -- sets it — COALESCE cannot express "set to NULL", so it uses the same CASE
    -- guard AdminUpdateUser uses for the tri-state quota.
    sensitive_content_policy = CASE WHEN sqlc.arg('set_sensitive_content_policy')::bool
                                    THEN sqlc.narg('sensitive_content_policy')
                                    ELSE sensitive_content_policy END,
    updated_at   = now()
WHERE id = sqlc.arg('id')
RETURNING id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes, unlisted, bypass_quarantine, deleted_at, pending_email_verification, history_enabled, profile_public, search_history_enabled, personalized_search_enabled, personalized_recommendations_enabled, sensitive_content_policy, show_bluesky, is_owner;

-- name: DeactivateUser :exec
UPDATE users
SET is_active  = FALSE,
    updated_at = now()
WHERE id = $1;

-- name: ListUsers :many
-- Admin user list: newest first, optionally filtered by a username/email
-- substring (empty query returns all). Paginated. Carries each account's
-- current storage usage (SUM of its video_files bytes via channel ownership)
-- so the admin view can show usage next to the quota.
SELECT u.id, u.username, u.email, u.password_hash, u.role, u.email_verified, u.is_active,
       u.created_at, u.updated_at, u.display_name, u.bio, u.storage_quota_bytes, u.unlisted,
       u.bypass_quarantine, u.deleted_at, u.pending_email_verification, u.history_enabled, u.profile_public,
       u.is_owner,
       (SELECT COALESCE(SUM(vf.size_bytes), 0)::bigint
          FROM video_files vf
          JOIN videos v ON v.id = vf.video_id
          JOIN channels c ON c.id = v.channel_id
         WHERE c.owner_id = u.id) AS storage_used_bytes
FROM users u
WHERE (sqlc.arg('query')::text = ''
       OR u.username ILIKE '%' || sqlc.arg('query')::text || '%'
       OR u.email ILIKE '%' || sqlc.arg('query')::text || '%')
ORDER BY u.created_at DESC, u.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountUsersMatching :one
-- How many accounts ListUsers would return for the same query, ignoring
-- pagination. The WHERE clause MUST stay identical to ListUsers': a total that
-- counts a different set than the page it labels is worse than no total, since
-- the admin UI derives its page count from it. CountUsers (unfiltered) is a
-- different question and is kept for callers that ask it.
SELECT count(*)
FROM users u
WHERE (sqlc.arg('query')::text = ''
       OR u.username ILIKE '%' || sqlc.arg('query')::text || '%'
       OR u.email ILIKE '%' || sqlc.arg('query')::text || '%');

-- name: AdminUpdateUser :one
-- Admin edit of a user's role, active flag, email_verified flag, quarantine
-- bypass, and/or storage quota (partial: NULL role/is_active/email_verified/
-- bypass_quarantine args are unchanged). The quota is tri-state — unchanged
-- unless set_storage_quota is true, in which case a NULL value resets the
-- account to the instance default and a value (0 = unlimited) overrides it.
UPDATE users
SET role       = COALESCE(sqlc.narg('role'), role),
    is_active  = COALESCE(sqlc.narg('is_active'), is_active),
    email_verified = COALESCE(sqlc.narg('email_verified'), email_verified),
    bypass_quarantine = COALESCE(sqlc.narg('bypass_quarantine'), bypass_quarantine),
    storage_quota_bytes = CASE WHEN sqlc.arg('set_storage_quota')::bool
                               THEN sqlc.narg('storage_quota_bytes')::bigint
                               ELSE storage_quota_bytes END,
    updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes, unlisted, bypass_quarantine, deleted_at, pending_email_verification, history_enabled, profile_public, search_history_enabled, personalized_search_enabled, personalized_recommendations_enabled, sensitive_content_policy, show_bluesky, is_owner;

-- name: AnonymizeDeletedUser :execrows
-- The §1 hard delete's final step: the users row is anonymised, NOT removed
-- (audit rows, DM sender identity, and comment tombstones keep resolving to a
-- placeholder). username/email become unique "deleted-…" sentinels supplied by
-- the caller, the password hash is CLEARED (nothing verifies against ""), the
-- profile is wiped, and the account is deactivated + stamped deleted_at.
-- Guarded on deleted_at IS NULL so it runs at most once (0 rows = already
-- deleted / unknown id).
UPDATE users
SET username      = sqlc.arg('username'),
    email         = sqlc.arg('email'),
    password_hash = '',
    display_name  = '',
    bio           = '',
    unlisted      = TRUE,
    is_active     = FALSE,
    deleted_at    = now(),
    updated_at    = now()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL;

-- name: SearchPublicAccounts :many
-- Account search (GET /api/v1/search/accounts), backed by core's own Postgres:
-- vidra-search does not index accounts at all, so there is no service path to
-- be consistent with.
--
-- VISIBILITY IS THE WHOLE POINT OF THIS QUERY. The gate is the one
-- GetPublicUserProfileByUsername already enforces, reproduced here so the two
-- can only ever agree:
--
--   is_active     = TRUE   deactivated, suspended (an admin clearing is_active),
--                          and hard-deleted accounts all collapse into this one
--                          flag, and all three are excluded.
--   profile_public = TRUE  the account opted its profile in. Without this, an
--                          account with no public page would still be
--                          enumerable by name, which is precisely the leak
--                          profile_public exists to prevent.
--
-- Plus one predicate the profile lookup does NOT have, and must not: NOT
-- unlisted. unlisted (§16) is the discovery opt-out — "keep serving my direct
-- URLs, keep me out of discovery". A profile fetched BY USERNAME is a direct
-- URL, so it ignores the flag; a search result list is discovery, so it honours
-- it, exactly as SearchPublicVideos already does for video owners. Applying the
-- profile rule alone here would put every unlisted account back into the one
-- surface it asked to leave.
--
-- Matching is unanchored and case-insensitive over username and display name
-- (0002 and 0118 index both for trigrams). The viewer's own mutes and blocks
-- are applied the same one-directional way as every other list.
SELECT u.id, u.username, u.display_name, u.bio, u.created_at
FROM users u
WHERE u.is_active = TRUE
  AND u.profile_public = TRUE
  AND NOT u.unlisted
  AND (u.username ILIKE '%' || sqlc.arg('query')::text || '%'
       OR u.display_name ILIKE '%' || sqlc.arg('query')::text || '%')
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = u.id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = u.id
  )
ORDER BY greatest(similarity(u.username, sqlc.arg('query')),
                  similarity(u.display_name, sqlc.arg('query'))) DESC,
         u.created_at DESC, u.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountSearchPublicAccounts :one
-- How many rows SearchPublicAccounts would return for the same query and
-- viewer, ignoring pagination. Every visibility and per-viewer predicate is
-- repeated VERBATIM: a total computed over a wider WHERE than the page would
-- leak the existence of the accounts it refuses to list, which is the same leak
-- from the other direction.
SELECT count(*)::bigint
FROM users u
WHERE u.is_active = TRUE
  AND u.profile_public = TRUE
  AND NOT u.unlisted
  AND (u.username ILIKE '%' || sqlc.arg('query')::text || '%'
       OR u.display_name ILIKE '%' || sqlc.arg('query')::text || '%')
  AND NOT EXISTS (
      SELECT 1 FROM muted_accounts m
      WHERE m.muter_id = sqlc.narg('viewer_id') AND m.muted_id = u.id
  )
  AND NOT EXISTS (
      SELECT 1 FROM user_blocks ub
      WHERE ub.blocker_id = sqlc.narg('viewer_id') AND ub.blocked_id = u.id
  );

-- name: CountActiveAdmins :one
-- How many accounts can still administer this instance. "Can still administer"
-- is deliberately narrow: role='admin' AND is_active AND not a tombstone —
-- exactly the set that can hold a session and reach an admin route. A deactivated
-- admin cannot sign in, and a tombstoned one cannot authenticate at all, so
-- neither counts as the safety net the last-admin guard is protecting.
SELECT count(*)::bigint
FROM users
WHERE role = 'admin' AND is_active AND deleted_at IS NULL;
