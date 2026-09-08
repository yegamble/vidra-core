-- 0137: cdn_purge_jobs — the durable queue behind every edge invalidation that
-- cannot finish inside the request that caused it.
--
-- WHY A TABLE AND NOT A GOROUTINE. Until this migration a purge was a detached
-- goroutine issuing one HTTP call per URL, once. Measured against a real caching
-- edge (docs/release-readiness.md, "A32/A33 delivery"), a rejected purge
-- produced one warning line and nothing else: no second attempt, no record, and
-- the edge still serving a deleted video's poster thirty seconds later. A
-- process restart lost even the intent. Three families need more than one pass:
--
--   - RETRY. An edge that answers 500 to eighteen purges is a transient outage,
--     not a permanent one, and the correct response is the same exponential
--     backoff every other queue in this schema already uses.
--   - ACCOUNT DELETION. One click cascades N channels' videos away; the URLs
--     have to be captured before the rows disappear and purged after, and an
--     api restart in between must not lose them.
--   - THE INSTANCE-WIDE downloads_enabled TOGGLE. Closing it revokes four-plus
--     URLs on EVERY public video at once. That is a walk over the catalogue, and
--     a walk needs a lease and a cursor or it is a fan-out that cannot be
--     resumed, paused or observed.
--
-- SHAPE. Deliberately account_exports (0057) / transcode_jobs (0039): the same
-- state machine (pending → running → done | failed), the same attempts +
-- next_attempt_at + last_error retry columns, the same lease. jobstatus reads it
-- as one more queue so it appears on the admin jobs page next to the others.
--
-- WHAT IS IN urls, AND WHY IT IS SAFE TO STORE. Media ROUTE PATHS of this api —
-- /api/v1/videos/<id>/thumbnail and the like — never purge endpoints and never
-- edge URLs. media_purge.go refuses to LOG a path because the operator-supplied
-- purge template can carry a credential in its query string; a route path
-- carries none, and every id in it is already a column of this database. The
-- DELIVERY_CDN_PURGE_* configuration never reaches this table.
CREATE TABLE cdn_purge_jobs (
    id               UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    -- 'urls' is a fixed list to invalidate (a retry, an account-deletion
    -- snapshot). 'downloads_revoked' is the resumable catalogue walk: its work
    -- is derived per batch from cursor_video_id rather than materialised up
    -- front, which is the whole reason it is a different kind.
    kind             TEXT        NOT NULL
                     CHECK (kind IN ('urls', 'downloads_revoked')),
    state            TEXT        NOT NULL DEFAULT 'pending'
                     CHECK (state IN ('pending', 'running', 'done', 'failed')),
    -- reason is the operator-facing label for why this job exists
    -- ('retry', 'account_delete', 'downloads_revoked'). It is a closed
    -- vocabulary this code writes, never free text from a request.
    reason           TEXT        NOT NULL DEFAULT '',
    -- urls: the remaining media route paths. A partially successful attempt
    -- REWRITES it to only what is still unpurged, so a retry never re-issues a
    -- call the edge already accepted and the column doubles as the progress
    -- record for kind='urls'.
    urls             JSONB       NOT NULL DEFAULT '[]'::jsonb
                     CHECK (jsonb_typeof(urls) = 'array'
                            AND octet_length(urls::text) <= 1048576),
    -- cursor_video_id: the last video id the walk finished, so a restart
    -- resumes after it rather than from the top. NULL = not started.
    cursor_video_id  UUID,
    -- url_set_complete carries the snapshot's own honesty forward: false means
    -- the list was already known to be short of what the edge could hold (a
    -- storage listing failed, the per-video cap was hit, or a superseded
    -- generation's ?v= tags could not be named). A job that purges every URL it
    -- holds is still incomplete if this is false, and the operator surface says
    -- so rather than reporting a clean run.
    url_set_complete BOOLEAN     NOT NULL DEFAULT TRUE,
    -- purged accumulates across attempts — the answer to "how much of this
    -- takedown has actually landed", which a per-attempt counter cannot give.
    purged           INT         NOT NULL DEFAULT 0 CHECK (purged >= 0),
    attempts         INT         NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    -- next_attempt_at is BOTH the retry schedule and the lease, which is the
    -- account_exports idiom (0057): a claim flips the row to 'running' and
    -- pushes this forward by the lease, so a worker killed mid-job leaves a row
    -- whose lease simply expires and which the next claim picks up. The claim
    -- scan therefore admits 'running' as well as 'pending' — that ONE
    -- difference from 0057 is what makes the catalogue walk resumable across a
    -- restart without a separate recovery sweep.
    next_attempt_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- last_error is a STATUS SUMMARY, never a URL and never a provider body
    -- (internal/cdn already strips both out of its errors before they surface).
    last_error       TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The worker's claim scan: due, still-claimable rows, oldest first.
CREATE INDEX cdn_purge_jobs_due_idx
    ON cdn_purge_jobs (next_attempt_at, id)
    WHERE state IN ('pending', 'running');

-- The operator surface: dead letters newest first, and the depth/oldest-pending
-- gauges the admin jobs page and `vidra doctor` read.
CREATE INDEX cdn_purge_jobs_state_created_idx
    ON cdn_purge_jobs (state, created_at DESC, id DESC);

-- ONE ACTIVE CATALOGUE WALK. The downloads_enabled toggle can be flipped shut
-- twice in a minute; the second flip must not start a second walk over the same
-- catalogue, because the walk is idempotent and a duplicate would only double
-- the third-party call volume. The enqueue is ON CONFLICT DO NOTHING against
-- this index.
CREATE UNIQUE INDEX cdn_purge_jobs_active_walk_idx
    ON cdn_purge_jobs (kind)
    WHERE kind = 'downloads_revoked' AND state IN ('pending', 'running');
