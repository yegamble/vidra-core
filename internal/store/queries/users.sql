-- name: GetUserByID :one
SELECT id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes, unlisted, bypass_quarantine, deleted_at, pending_email_verification, history_enabled, profile_public, search_history_enabled, personalized_search_enabled, personalized_recommendations_enabled, sensitive_content_policy, show_bluesky
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
SELECT id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes, unlisted, bypass_quarantine, deleted_at, pending_email_verification, history_enabled, profile_public, search_history_enabled, personalized_search_enabled, personalized_recommendations_enabled, sensitive_content_policy, show_bluesky
FROM users
WHERE lower(email) = lower($1);

-- name: GetUserByUsername :one
-- Resolve a username to a full user row, case-insensitive and active-only
-- (deactivated accounts are treated as not found → the caller 404s, so an
-- inactive account's existence is not leaked differently from an unknown one).
-- Used to start a DM by username instead of by id.
SELECT id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes, unlisted, bypass_quarantine, deleted_at, pending_email_verification, history_enabled, profile_public, search_history_enabled, personalized_search_enabled, personalized_recommendations_enabled, sensitive_content_policy, show_bluesky
FROM users
WHERE lower(username) = lower($1) AND is_active = true;

-- name: CreateUser :one
-- pending_email_verification is TRUE only when the account is created while
-- the registration email-verification gate is active (W7 — the grandfather
-- clause lives in this flag, never in a retroactive check); history_enabled
-- is seeded from the new_user_history_enabled instance setting.
INSERT INTO users (username, email, password_hash, role, pending_email_verification, history_enabled)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes, unlisted, bypass_quarantine, deleted_at, pending_email_verification, history_enabled, profile_public, search_history_enabled, personalized_search_enabled, personalized_recommendations_enabled, sensitive_content_policy, show_bluesky;

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
RETURNING id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes, unlisted, bypass_quarantine, deleted_at, pending_email_verification, history_enabled, profile_public, search_history_enabled, personalized_search_enabled, personalized_recommendations_enabled, sensitive_content_policy, show_bluesky;

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
       (SELECT COALESCE(SUM(vf.size_bytes), 0)::bigint
          FROM video_files vf
          JOIN videos v ON v.id = vf.video_id
          JOIN channels c ON c.id = v.channel_id
         WHERE c.owner_id = u.id) AS storage_used_bytes
FROM users u
WHERE (sqlc.arg('query')::text = ''
       OR u.username ILIKE '%' || sqlc.arg('query') || '%'
       OR u.email ILIKE '%' || sqlc.arg('query') || '%')
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
       OR u.username ILIKE '%' || sqlc.arg('query') || '%'
       OR u.email ILIKE '%' || sqlc.arg('query') || '%');

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
RETURNING id, username, email, password_hash, role, email_verified, is_active, created_at, updated_at, display_name, bio, storage_quota_bytes, unlisted, bypass_quarantine, deleted_at, pending_email_verification, history_enabled, profile_public, search_history_enabled, personalized_search_enabled, personalized_recommendations_enabled, sensitive_content_policy, show_bluesky;

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
