-- 0079: import-job resolver + progress stage (backport W2.C1, UPLOAD-09).
--
-- The async URL-import queue (0059) grows a `resolver` and a `stage` so a single
-- import job can be fetched either as a plain media file (the existing `direct`
-- path) or through the sandboxed yt-dlp platform extractor (`ytdlp`), and so the
-- UI can show honest per-stage progress.
--
-- resolver — how the worker fetches the source:
--   'direct'  a plain HTTP(S) media file (the pre-W2 behaviour; the DEFAULT so
--             every pre-existing row keeps its exact semantics on backfill).
--   'ytdlp'   the yt-dlp extractor (metadata probe → bounded download), gated by
--             YTDLP_IMPORT_ENABLED.
--   'auto'    the REQUESTED value only: the worker resolves it to a concrete
--             'direct'|'ytdlp' during the 'resolving' stage (content-type probe,
--             then yt-dlp fallback when enabled) and rewrites this column to the
--             concrete choice so the stored value is always what actually ran.
-- The CHECK admits 'auto' as the transient requested value (the sketch in the
-- W2 spec listed only 'direct'|'ytdlp'; it is widened here so resolution can be
-- deferred to the worker, which is what makes the 'resolving' stage meaningful).
--
-- stage — coarse progress for the UI while state='running' (empty otherwise):
--   ''            queued (not yet claimed)
--   'resolving'   picking the concrete resolver (auto only)
--   'downloading' fetching the bytes (direct fetch or yt-dlp download)
--   'processing'  bytes handed to AttachOriginal → Process (scan/probe/transcode)
-- stage never carries a secret; `error` remains the only failure channel and is
-- always a SAFE, client-visible reason (never a raw error or the source URL).
ALTER TABLE import_jobs
    ADD COLUMN resolver TEXT NOT NULL DEFAULT 'direct'
        CHECK (resolver IN ('auto', 'direct', 'ytdlp')),
    ADD COLUMN stage    TEXT NOT NULL DEFAULT '';
