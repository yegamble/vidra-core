-- 0100: granular sensitive-content handling (PeerTube v7.2 parity).
--   * users.sensitive_content_policy — a per-user override of the instance-wide
--     sensitive_content_policy (hide|warn|blur|display). NULL means "inherit the
--     instance default" (the common case); a non-NULL value wins for that user.
--     Only "hide" is server-enforced (flagged videos drop out of that user's
--     browse/search surfaces); warn/blur/display are presentation-only hints.
--   * videos.sensitive_reason — the creator's optional free-text content warning
--     shown next to a sensitive video (empty string when unset). Stored
--     regardless of is_sensitive: the frontend pairs the two.
ALTER TABLE users
    ADD COLUMN sensitive_content_policy TEXT
        CHECK (sensitive_content_policy IN ('hide', 'warn', 'blur', 'display'));
ALTER TABLE videos
    ADD COLUMN sensitive_reason TEXT NOT NULL DEFAULT '';
