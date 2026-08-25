-- 0115: record the per-run acknowledgement of an unverified PeerTube schema, and
-- give a failed run a machine-readable reason.
--
-- Preflight refuses a source whose application.migrationVersion falls outside the
-- verified range. That refusal is correct and stays. What was missing is a way
-- for an ADMIN to sign off on it: --force is a CLI flag, the API worker has never
-- had one, and so the whole web UI was unusable against a real 8.x source (the
-- instance that forced this reported 1040 against a ceiling of 1000) — the
-- operator had to hand-build the CLI, scp it to the host and tunnel to the
-- database instead.
--
-- acknowledged_schema_version is that sign-off, and it is a VERSION rather than a
-- boolean on purpose: the gate only opens when it equals the version preflight
-- actually detected, so it cannot be set in the abstract, cannot be carried
-- forward by a caller that never looked, and stops applying the moment the source
-- moves. NULL is the norm — no acknowledgement was made. It lives on the run row
-- because a run is the only scope this may ever have: it is never a setting,
-- never a default, and the row already records started_by, so who acknowledged
-- and what they acknowledged are one fact stored once.
--
-- error_code is the stable snake_case reason a run failed, matching the
-- error_code column operational_job_runs (0083) already carries. Without it the
-- only thing distinguishing "your source schema is unverified, here is the
-- version" from "the source database is unreachable" is English prose in `error`,
-- which no client can branch on.
ALTER TABLE peertube_import_runs
    ADD COLUMN acknowledged_schema_version INTEGER
        CHECK (acknowledged_schema_version IS NULL OR acknowledged_schema_version > 0),
    ADD COLUMN error_code TEXT NOT NULL DEFAULT ''
        CHECK (length(error_code) <= 128);
