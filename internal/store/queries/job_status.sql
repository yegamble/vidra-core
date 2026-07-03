-- Job-status aggregation for the admin operations dashboard (fix_plan P17.4).
-- One aggregate query per durable queue returns the normalised
-- {pending, running, done, failed, oldest_pending_age_seconds} shape the admin
-- jobs page consumes; a per-queue recent-failures query lists the latest dead
-- rows (id, error, attempts) with NO secrets (never the inbox URL, source URL,
-- storage key, or arguments). Queues use slightly different state vocabularies
-- (delivered vs done; active/completed/cancelled for sessions); each query
-- normalises to the common four states here so the service layer stays trivial.

-- name: TranscodeJobStats :one
SELECT
    count(*) FILTER (WHERE state = 'pending')::bigint AS pending,
    count(*) FILTER (WHERE state = 'running')::bigint AS running,
    count(*) FILTER (WHERE state = 'done')::bigint    AS done,
    count(*) FILTER (WHERE state = 'failed')::bigint  AS failed,
    COALESCE(EXTRACT(EPOCH FROM (now() - min(created_at) FILTER (WHERE state = 'pending')))::bigint, 0)::bigint AS oldest_pending_age_seconds
FROM transcode_jobs;

-- name: FederationDeliveryStats :one
SELECT
    count(*) FILTER (WHERE state = 'pending')::bigint   AS pending,
    count(*) FILTER (WHERE state = 'running')::bigint   AS running,
    count(*) FILTER (WHERE state = 'delivered')::bigint AS done,
    count(*) FILTER (WHERE state = 'failed')::bigint    AS failed,
    COALESCE(EXTRACT(EPOCH FROM (now() - min(created_at) FILTER (WHERE state = 'pending')))::bigint, 0)::bigint AS oldest_pending_age_seconds
FROM federation_deliveries;

-- name: ImportJobStats :one
SELECT
    count(*) FILTER (WHERE state = 'pending')::bigint AS pending,
    count(*) FILTER (WHERE state = 'running')::bigint AS running,
    count(*) FILTER (WHERE state = 'done')::bigint    AS done,
    count(*) FILTER (WHERE state = 'failed')::bigint  AS failed,
    COALESCE(EXTRACT(EPOCH FROM (now() - min(created_at) FILTER (WHERE state = 'pending')))::bigint, 0)::bigint AS oldest_pending_age_seconds
FROM import_jobs;

-- name: CaptionJobStats :one
SELECT
    count(*) FILTER (WHERE state = 'pending')::bigint AS pending,
    count(*) FILTER (WHERE state = 'running')::bigint AS running,
    count(*) FILTER (WHERE state = 'done')::bigint    AS done,
    count(*) FILTER (WHERE state = 'failed')::bigint  AS failed,
    COALESCE(EXTRACT(EPOCH FROM (now() - min(created_at) FILTER (WHERE state = 'pending')))::bigint, 0)::bigint AS oldest_pending_age_seconds
FROM caption_jobs;

-- name: AccountExportStats :one
SELECT
    count(*) FILTER (WHERE state = 'pending')::bigint AS pending,
    count(*) FILTER (WHERE state = 'running')::bigint AS running,
    count(*) FILTER (WHERE state = 'done')::bigint    AS done,
    count(*) FILTER (WHERE state = 'failed')::bigint  AS failed,
    COALESCE(EXTRACT(EPOCH FROM (now() - min(created_at) FILTER (WHERE state = 'pending')))::bigint, 0)::bigint AS oldest_pending_age_seconds
FROM account_exports;

-- name: UploadSessionStats :one
SELECT
    count(*) FILTER (WHERE state = 'active')::bigint    AS pending,
    0::bigint                                           AS running,
    count(*) FILTER (WHERE state = 'completed')::bigint AS done,
    count(*) FILTER (WHERE state = 'cancelled')::bigint AS failed,
    COALESCE(EXTRACT(EPOCH FROM (now() - min(created_at) FILTER (WHERE state = 'active')))::bigint, 0)::bigint AS oldest_pending_age_seconds
FROM upload_sessions;

-- name: TranscodeRecentFailures :many
SELECT id, last_error AS error, attempts, updated_at
FROM transcode_jobs
WHERE state = 'failed'
ORDER BY updated_at DESC
LIMIT $1;

-- name: FederationRecentFailures :many
SELECT id, last_error AS error, attempts, updated_at
FROM federation_deliveries
WHERE state = 'failed'
ORDER BY updated_at DESC
LIMIT $1;

-- name: ImportRecentFailures :many
SELECT id, error, attempts, updated_at
FROM import_jobs
WHERE state = 'failed'
ORDER BY updated_at DESC
LIMIT $1;

-- name: CaptionRecentFailures :many
SELECT id, error, attempts, updated_at
FROM caption_jobs
WHERE state = 'failed'
ORDER BY updated_at DESC
LIMIT $1;

-- name: AccountExportRecentFailures :many
SELECT id, last_error AS error, attempts, updated_at
FROM account_exports
WHERE state = 'failed'
ORDER BY updated_at DESC
LIMIT $1;
