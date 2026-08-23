-- 0109: Playback quality-of-experience telemetry (phase-4 delivery item 4,
-- docs/productionization/interfaces.md §9).
--
-- Two tables, deliberately NOT one. search_outbox — the pattern §9 originally
-- pointed at — is an egress queue to an external service and prunes nothing:
-- there is no DELETE against it anywhere. That is survivable at search volume
-- and is not at playback volume, where every viewer produces events for the
-- whole watch. So the shape here is raw -> rollup -> prune:
--
--   qoe_events   individual measurements, SHORT retention (7 days). This is the
--                incident table: when a rebuffer spike shows up in the rollups,
--                this is what still has the per-event detail behind it.
--   qoe_rollups  one row per (hour, delivery_source, engine, packaging_format),
--                LONG retention (90 days), carrying counts, precomputed
--                p50/p95/p99 and the mergeable histograms they came from.
--
-- The exit criterion is "an admin can see TTFF/rebuffer percentiles per source
-- for the last 24h". That answer must not be a scan of an unbounded table, so
-- it is served from qoe_rollups. Percentiles do not merge — a p95 of 24 hourly
-- p95s is not the p95 of the day — which is why each rollup row also stores the
-- fixed-boundary HISTOGRAM the percentiles were computed from. Histograms DO
-- merge (they are just counts), so summing them across hours, engines and
-- packaging formats yields a per-source window percentile that is correct to
-- the histogram's bucket resolution rather than fabricated.
--
-- Retention is enforced by a leader-elected prune worker modelled on
-- jobstatus.Prune (fixed windows, 10k-row batches), NOT by this migration.

-- ---------------------------------------------------------------------------
-- Raw events
-- ---------------------------------------------------------------------------
--
-- received_at is the SERVER's receipt time, never a client-supplied timestamp.
-- A client clock is unbounded and spoofable, and a beacon that could name its
-- own hour bucket could rewrite an hour that was already rolled up. The cost is
-- a one-row skew at hour boundaries (a measurement taken at 10:59:59 and
-- beaconed at 11:00:01 lands in the 11:00 bucket), which is honest and cheap;
-- the benefit is that a rolled-up hour is final and late arrivals do not exist.
--
-- There is no user_id column, on purpose. A row here says "someone saw this
-- video and it took this long to start"; adding an account id would make this a
-- second watch-history table with a different retention policy and no user
-- control over it. viewer_digest carries exactly the distinguishing power an
-- incident needs (was that spike one viewer or a thousand) and nothing else --
-- see internal/qoe/digest.go for its construction and rotation policy.
CREATE TABLE qoe_events (
    id                UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Closed vocabularies. They are CHECKed here as defence in depth; the
    -- authoritative rejection happens at the API edge (internal/qoe), because a
    -- 400 telling a client its value is unknown is worth more than a constraint
    -- violation swallowed by a best-effort writer.
    event_type        TEXT        NOT NULL
                                  CHECK (event_type IN ('playback.start', 'playback.rebuffer',
                                                        'playback.bitrate_switch', 'playback.error')),
    delivery_source   TEXT        NOT NULL
                                  CHECK (delivery_source IN ('api-proxy', 'presigned', 'cdn',
                                                             'ipfs-gateway', 'origin-live', 'other')),
    engine            TEXT        NOT NULL
                                  CHECK (engine IN ('hls-js', 'native-hls', 'progressive', 'shaka')),
    packaging_format  TEXT        NOT NULL
                                  CHECK (packaging_format IN ('hls-ts', 'cmaf', 'progressive')),

    -- The subject. video_id is nullable so a live session (phase-4 item 7, whose
    -- delivery_source is 'origin-live') can be recorded without lying about
    -- which table its subject lives in.
    video_id          UUID        REFERENCES videos(id) ON DELETE CASCADE,
    live_stream_id    UUID        REFERENCES live_streams(id) ON DELETE CASCADE,

    -- session_id correlates every event of one playback. core#74 minted session
    -- ids but recorded none, so for a public video this value is CLIENT-ASSERTED
    -- and an admin must not read it as attested. session_verified is true only
    -- when the beacon also carried the HMAC-signed playback token that contains
    -- this same session id -- which today happens exactly for password videos
    -- and private live streams. Recording the distinction is what lets the admin
    -- view state how much of a number is attested instead of implying all of it.
    session_id        UUID,
    session_verified  BOOLEAN     NOT NULL DEFAULT false,

    -- Keyed, day-scoped, domain-separated digest of the viewer. Never an IP,
    -- never an account id, never correlatable across UTC days or across a
    -- JWT_SECRET rotation. See internal/qoe/digest.go.
    viewer_digest     TEXT        NOT NULL DEFAULT '',

    -- Measurements. Each is NULL for the event types that do not carry it;
    -- NULL means "this event type does not measure that", not "zero".
    ttff_ms           INTEGER     CHECK (ttff_ms IS NULL OR ttff_ms >= 0),
    rebuffer_ms       INTEGER     CHECK (rebuffer_ms IS NULL OR rebuffer_ms >= 0),

    -- The height of the rendition in play, when it is knowable. On native HLS it
    -- is PERMANENTLY unknown: the browser owns variant selection through the
    -- manifest's SCORE attribute and the adapter can neither read nor set the
    -- active variant. That is why this is nullable and why 'engine' is part of
    -- the rollup key -- an engine='native-hls' row with no rendition data is
    -- correct, not missing.
    rendition_height  INTEGER     CHECK (rendition_height IS NULL OR rendition_height > 0),

    -- Closed error vocabulary, NULL except on playback.error.
    error_class       TEXT        CHECK (error_class IS NULL OR error_class IN
                                         ('network', 'media', 'manifest', 'decrypt', 'timeout', 'other')),

    -- Deny-by-default allowlisted, size-capped, redacted extras (jobstatus's
    -- safeMetadata discipline). Free-form client text never lands here.
    metadata          JSONB       NOT NULL DEFAULT '{}'
);

-- The rollup worker's paged read and the prune worker's batch both walk this
-- table in (received_at, id) order. id is a UUID, so ORDER BY id is random and
-- would make keyset paging return rows in an order unrelated to time; the
-- composite is the only stable cursor here.
CREATE INDEX qoe_events_received_idx ON qoe_events (received_at, id);

-- Incident lookup: "what happened to this video / this playback".
CREATE INDEX qoe_events_video_idx ON qoe_events (video_id, received_at)
    WHERE video_id IS NOT NULL;
CREATE INDEX qoe_events_session_idx ON qoe_events (session_id, received_at)
    WHERE session_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Hourly rollups
-- ---------------------------------------------------------------------------
--
-- The primary key IS the bounded-cardinality rule expressed in the schema: four
-- closed vocabularies, so the row count per hour has a hard ceiling
-- (6 sources x 4 engines x 3 formats = 72) no matter how much traffic arrives.
-- Nothing free-form is ever part of this key, which is what keeps 90 days of
-- history small enough to query without an aggregate index.
CREATE TABLE qoe_rollups (
    hour_bucket        TIMESTAMPTZ NOT NULL,
    delivery_source    TEXT        NOT NULL,
    engine             TEXT        NOT NULL,
    packaging_format   TEXT        NOT NULL,

    event_count            BIGINT  NOT NULL DEFAULT 0,
    start_count            BIGINT  NOT NULL DEFAULT 0,
    rebuffer_count         BIGINT  NOT NULL DEFAULT 0,
    bitrate_switch_count   BIGINT  NOT NULL DEFAULT 0,
    error_count            BIGINT  NOT NULL DEFAULT 0,
    -- How many of this bucket's events carried a session id the server could
    -- verify against a signed token. Below event_count means the rest are
    -- client-asserted.
    verified_count         BIGINT  NOT NULL DEFAULT 0,

    -- Precomputed percentiles, in milliseconds. NULL when the bucket recorded no
    -- measurement of that kind -- "nobody reported a rebuffer" is not "0 ms".
    ttff_p50_ms        INTEGER,
    ttff_p95_ms        INTEGER,
    ttff_p99_ms        INTEGER,
    rebuffer_p50_ms    INTEGER,
    rebuffer_p95_ms    INTEGER,
    rebuffer_p99_ms    INTEGER,
    -- Total rebuffered milliseconds in the bucket, so a rebuffer RATIO is
    -- derivable without going back to the raw table.
    rebuffer_total_ms  BIGINT      NOT NULL DEFAULT 0,

    -- The distributions the percentiles above were computed from: bucket counts
    -- over the fixed, code-owned boundaries in internal/qoe/histogram.go.
    -- Percentiles do not merge; these do. histogram_version pins which boundary
    -- table produced them, so the boundaries can change later without silently
    -- reinterpreting old rows.
    histogram_version  SMALLINT    NOT NULL DEFAULT 1,
    ttff_histogram     BIGINT[]    NOT NULL DEFAULT '{}',
    rebuffer_histogram BIGINT[]    NOT NULL DEFAULT '{}',

    -- Per-error-class counts, keyed by the closed error vocabulary. A JSONB map
    -- rather than error_class in the primary key: putting it in the key would
    -- multiply the row ceiling by the vocabulary size for a dimension nobody
    -- slices percentiles by.
    error_counts       JSONB       NOT NULL DEFAULT '{}',

    computed_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (hour_bucket, delivery_source, engine, packaging_format)
);

-- The admin window query ("the last 24h") is a range scan on hour_bucket; the
-- primary key already leads with it. This index serves the prune worker's
-- oldest-first batch, which needs hour_bucket alone.
CREATE INDEX qoe_rollups_hour_idx ON qoe_rollups (hour_bucket);
