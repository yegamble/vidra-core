-- Playback quality-of-experience telemetry (migration 0109, phase-4 delivery
-- item 4). Raw events -> hourly rollups -> prune, with the rollup arithmetic
-- deliberately in Go (internal/qoe) rather than in percentile_cont here: the
-- histograms that make a window percentile mergeable have to be built anyway,
-- and building them in Go is what makes the arithmetic testable in `make ci`,
-- which runs without a database.

-- name: InsertQoEEvent :exec
-- Best-effort append of one measurement. The caller logs + meters on failure and
-- NEVER fails the request path: a QoE beacon is non-authoritative telemetry and
-- a viewer's playback must not depend on it being recorded.
INSERT INTO qoe_events (
    event_type, delivery_source, engine, packaging_format,
    video_id, live_stream_id, session_id, session_verified, viewer_digest,
    ttff_ms, rebuffer_ms, rendition_height, error_class, metadata
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
);

-- name: LatestQoERollupHour :many
-- The newest hour already rolled up, or no rows at all when nothing has been.
-- The watermark is DERIVED rather than kept in a progress row: qoe_rollups only
-- ever gains rows for COMPLETE hours, so its newest hour_bucket is the watermark
-- by construction and there is no second piece of state that could disagree with
-- it. :many with LIMIT 1 rather than :one so "nothing yet" is an empty result
-- instead of a NULL that has to be given a sentinel value.
SELECT hour_bucket FROM qoe_rollups ORDER BY hour_bucket DESC LIMIT 1;

-- name: ListQoERollupBuckets :many
-- The complete hours that still need rolling up, oldest first. Buckets are
-- discovered from the events themselves rather than generated, so an hour with
-- no playback costs nothing and does not stall the watermark.
--
-- after_bucket is the watermark + 1h, or the raw-retention floor on an install
-- that has never rolled up. Either way the scan is bounded by the retention
-- window and rides qoe_events_received_idx.
SELECT date_trunc('hour', received_at)::timestamptz AS hour_bucket
FROM qoe_events
WHERE received_at >= sqlc.arg('after_bucket')
  AND received_at < sqlc.arg('complete_before')
GROUP BY 1
ORDER BY 1
LIMIT sqlc.arg('max_buckets');

-- name: ListQoEEventsForBucketPage :many
-- One keyset page of an hour bucket's raw measurements, for the Go-side rollup.
--
-- The cursor is (received_at, id) and NOT id: id is a UUID, so ORDER BY id is
-- effectively random and a page boundary taken on it would neither be stable nor
-- correspond to time. Pass '-infinity' + the nil UUID for the first page.
SELECT id, received_at, event_type, delivery_source, engine, packaging_format,
       session_verified, ttff_ms, rebuffer_ms, error_class
FROM qoe_events
WHERE received_at >= sqlc.arg('bucket_start')
  AND received_at < sqlc.arg('bucket_end')
  AND (received_at, id) > (sqlc.arg('after_received_at')::timestamptz, sqlc.arg('after_id')::uuid)
ORDER BY received_at, id
LIMIT sqlc.arg('page_size');

-- name: UpsertQoERollup :exec
-- Writes one (hour, source, engine, format) rollup. The upsert is what makes the
-- rollup worker safe to re-run: leadership can move mid-sweep, and recomputing a
-- bucket from its (immutable, complete) raw rows must land on the same numbers
-- rather than doubling them, which an INSERT ... ON CONFLICT DO UPDATE with
-- assignment -- not addition -- guarantees.
INSERT INTO qoe_rollups (
    hour_bucket, delivery_source, engine, packaging_format,
    event_count, start_count, rebuffer_count, bitrate_switch_count, error_count,
    verified_count,
    ttff_p50_ms, ttff_p95_ms, ttff_p99_ms,
    rebuffer_p50_ms, rebuffer_p95_ms, rebuffer_p99_ms, rebuffer_total_ms,
    histogram_version, ttff_histogram, rebuffer_histogram, error_counts,
    computed_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17,
    $18, $19, $20, $21, now()
)
ON CONFLICT (hour_bucket, delivery_source, engine, packaging_format) DO UPDATE SET
    event_count          = EXCLUDED.event_count,
    start_count          = EXCLUDED.start_count,
    rebuffer_count       = EXCLUDED.rebuffer_count,
    bitrate_switch_count = EXCLUDED.bitrate_switch_count,
    error_count          = EXCLUDED.error_count,
    verified_count       = EXCLUDED.verified_count,
    ttff_p50_ms          = EXCLUDED.ttff_p50_ms,
    ttff_p95_ms          = EXCLUDED.ttff_p95_ms,
    ttff_p99_ms          = EXCLUDED.ttff_p99_ms,
    rebuffer_p50_ms      = EXCLUDED.rebuffer_p50_ms,
    rebuffer_p95_ms      = EXCLUDED.rebuffer_p95_ms,
    rebuffer_p99_ms      = EXCLUDED.rebuffer_p99_ms,
    rebuffer_total_ms    = EXCLUDED.rebuffer_total_ms,
    histogram_version    = EXCLUDED.histogram_version,
    ttff_histogram       = EXCLUDED.ttff_histogram,
    rebuffer_histogram   = EXCLUDED.rebuffer_histogram,
    error_counts         = EXCLUDED.error_counts,
    computed_at          = now();

-- name: ListQoERollups :many
-- The admin playback-health window, oldest hour first. Bounded by the caller's
-- window and by a row cap; the closed vocabularies cap how many rows an hour can
-- ever produce, so a 24h window is at most 24 x 72 rows.
SELECT hour_bucket, delivery_source, engine, packaging_format,
       event_count, start_count, rebuffer_count, bitrate_switch_count, error_count,
       verified_count,
       ttff_p50_ms, ttff_p95_ms, ttff_p99_ms,
       rebuffer_p50_ms, rebuffer_p95_ms, rebuffer_p99_ms, rebuffer_total_ms,
       histogram_version, ttff_histogram, rebuffer_histogram, error_counts,
       computed_at
FROM qoe_rollups
WHERE hour_bucket >= sqlc.arg('window_start')
  AND hour_bucket < sqlc.arg('window_end')
ORDER BY hour_bucket, delivery_source, engine, packaging_format
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountQoERollups :one
SELECT count(*)::bigint
FROM qoe_rollups
WHERE hour_bucket >= sqlc.arg('window_start')
  AND hour_bucket < sqlc.arg('window_end');

-- name: PruneQoEEvents :execrows
-- One batch of expired raw measurements, oldest first. The subselect orders by
-- (received_at, id) for the same reason the rollup page does: on a UUID key,
-- ORDER BY id is random, and a "delete the oldest 10k" that deletes an arbitrary
-- 10k leaves the oldest rows alive forever.
DELETE FROM qoe_events
WHERE id IN (
    SELECT e.id FROM qoe_events e
    WHERE e.received_at < sqlc.arg('cutoff')
    ORDER BY e.received_at, e.id
    LIMIT sqlc.arg('batch_size')
);

-- name: PruneQoERollups :execrows
DELETE FROM qoe_rollups
WHERE (hour_bucket, delivery_source, engine, packaging_format) IN (
    SELECT r.hour_bucket, r.delivery_source, r.engine, r.packaging_format
    FROM qoe_rollups r
    WHERE r.hour_bucket < sqlc.arg('cutoff')
    ORDER BY r.hour_bucket
    LIMIT sqlc.arg('batch_size')
);
