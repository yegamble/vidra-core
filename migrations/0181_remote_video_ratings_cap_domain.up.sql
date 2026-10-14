-- 0181: key the per-domain cap on new remote votes by REGISTRABLE domain.
-- remote_video_ratings.domain is the signer's exact host and must stay that:
-- the blocked_instances read filter in CountVideoRatings matches it exactly.
-- But the cap keyed on it too, so sub1..subN.evil.example each got their own
-- hourly budget. cap_domain is the separate cap key (the registrable domain,
-- eTLD+1), so one operator's subdomains share one budget.
--
-- Backfill cap_domain = domain: the cap only counts the last hour, so rows
-- older than that never matter and the exact host is a safe stand-in.
ALTER TABLE remote_video_ratings ADD COLUMN cap_domain TEXT;
UPDATE remote_video_ratings SET cap_domain = domain;
-- Nullable on purpose: the previous release still inserts rows without it
-- (one-release schema compat). Such a row is simply not counted by the cap,
-- which only matters for the hour of a rolling deploy.
ALTER TABLE remote_video_ratings
    ADD CONSTRAINT remote_video_ratings_cap_domain_len CHECK (char_length(cap_domain) BETWEEN 1 AND 255);

CREATE INDEX remote_video_ratings_cap_domain_created_idx ON remote_video_ratings (cap_domain, created_at);
