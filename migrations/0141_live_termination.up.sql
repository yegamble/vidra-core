-- 0141: Moderator termination of a live broadcast.
--
-- A26 measured the gap this closes: there was NO control anywhere that ended a
-- live stream. Core registered no /admin/live* route, DELETE/PATCH /live/{id}
-- are owner-scoped and 404 for an admin, and the only lever was the GLOBAL
-- live_max_duration_secs watchdog — 60 s floor, up to one 30 s sweep late, and
-- applied to every stream on the instance at once. A moderator watching an
-- instance-damaging broadcast could change a global setting, delete the
-- account, or nothing.
--
-- The termination itself is a state flip the existing watchdog path already
-- performs. What needs storing is the ANSWER TO THE CREATOR: a stream that
-- simply went 'ended' is indistinguishable from one whose publisher
-- disconnected, so without these columns the creator is told nothing and the
-- moderation action is invisible to the only person it is aimed at (the same
-- loop A16 closed for a blocked video).
--
-- Two fields, deliberately, and the split matters:
--
--   termination_reason_code — a CLOSED SET (see internal/live.TerminationReasons)
--     and the only part that reaches the audit trail. audit_log cannot carry
--     prose (A16), so a stable code is what an operator can filter on and what
--     the UI renders as a sentence.
--   termination_reason — the moderator's FREE TEXT, stored exactly where a
--     video block stores its reason: on the row, never in the audit envelope.
--
-- Nullable/empty by default and CLEARED on the next go-live: a terminated
-- permanent stream that is allowed to broadcast again must not carry last
-- month's reason on its page forever (see SetLiveStreamState).
ALTER TABLE live_streams
    ADD COLUMN terminated_at           TIMESTAMPTZ,
    ADD COLUMN terminated_by           UUID REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN termination_reason_code TEXT,
    ADD COLUMN termination_reason      TEXT NOT NULL DEFAULT '';

-- The code set is enforced in the database as well as in Go. The Go allow-list
-- is what returns a 422 to the moderator; this is what stops a future writer
-- (a backfill, a console session, a second code path) from inventing a code the
-- UI has no sentence for. NULL is allowed — it is the "not terminated" state,
-- and it is also what an OWNER's own end-stream leaves behind, since the owner
-- ending their own broadcast is not a moderation action and has no reason.
ALTER TABLE live_streams
    ADD CONSTRAINT live_streams_termination_reason_code_check
    CHECK (
        termination_reason_code IS NULL
        OR termination_reason_code IN (
            'policy_violation',
            'copyright',
            'sensitive_content',
            'spam',
            'harassment',
            'legal_request',
            'technical',
            'other'
        )
    );
