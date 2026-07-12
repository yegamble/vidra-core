-- 0089: per-video publish policies (config-parity W9;
-- .ralph/specs/config-parity/waves.md).
--
-- Two per-video policy columns backing the defaults.publish admin settings:
--
--   comments_policy  — whether NEW comments may be posted on this video
--                      ('enabled' | 'disabled'). PeerTube's third tier
--                      'requires_approval' is deliberately deferred (no
--                      comment-approval queue exists; see the parity ledger
--                      §13). Reading existing comments stays open either way,
--                      mirroring the instance-wide comments_enabled toggle.
--   download_enabled — whether official download routes serve this video's
--                      files. LAYERS on the instance-wide downloads_enabled
--                      setting (84b5a38): instance gate off => no downloads
--                      regardless; instance gate on => this flag decides.
--                      Moderators/admins bypass both for moderation.
--
-- Backfill semantics: existing videos keep behaving exactly as before —
-- comments allowed, downloads allowed — so both columns default to the
-- shipped behaviour. New videos are seeded from the default_comment_policy /
-- default_download_enabled instance settings at create time (handler-side).
ALTER TABLE videos
    ADD COLUMN comments_policy text NOT NULL DEFAULT 'enabled'
        CHECK (comments_policy IN ('enabled', 'disabled')),
    ADD COLUMN download_enabled boolean NOT NULL DEFAULT true;
