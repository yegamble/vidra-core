-- 0090: video file replacement (config-parity W14).
--
-- purpose — what completing an upload session does with the assembled bytes:
--   'upload'  — the shipped behaviour: attach the original to a draft video and
--               run it through the publish pipeline (AttachOriginal → Process).
--   'replace' — replace the source of an ALREADY PUBLISHED video with a new
--               version (ReplaceSource): the video row, its URLs and metadata
--               stay stable; the old source + renditions keep serving until the
--               re-transcode promotes the new generation.
-- Every existing row backfills to 'upload' (the pre-W14 behaviour).
ALTER TABLE upload_sessions
    ADD COLUMN purpose TEXT NOT NULL DEFAULT 'upload'
    CHECK (purpose IN ('upload', 'replace'));
