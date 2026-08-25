-- 0118: trigram indexes on the two DISPLAY NAME columns, to back the new
-- channel- and account-search endpoints (GET /api/v1/search/channels and
-- /api/v1/search/accounts). pg_trgm is enabled in 0001.
--
-- The identifier halves of those searches are already indexed: 0002 put a trgm
-- index on users.username and 0003 one on channels.handle. The display names
-- were never indexed because nothing searched them — every previous lookup was
-- an exact handle/username resolve.
--
-- Both new queries match `handle ILIKE %q% OR display_name ILIKE %q%`. With only
-- one side of that OR indexed Postgres cannot use the index at all: it has no
-- way to satisfy the other branch except by reading every row, so it seq-scans
-- the whole table and the existing index contributes nothing. Indexing the
-- second column is what turns the disjunction into a BitmapOr of two index
-- scans. That is also why this is one migration and not two — either index
-- alone leaves the plan unchanged.
--
-- gin_trgm_ops (not btree) because the predicate is an unanchored ILIKE and the
-- ORDER BY is similarity(); a btree index answers neither.
CREATE INDEX channels_display_name_trgm_idx
    ON channels USING gin (display_name gin_trgm_ops);

CREATE INDEX users_display_name_trgm_idx
    ON users USING gin (display_name gin_trgm_ops);
