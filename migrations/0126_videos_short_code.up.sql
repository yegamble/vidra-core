-- 0126: give every video an opaque 11-character short code, the identifier the
-- public watch URL is built from (/v/{code}).
--
-- WHY A STORED CODE AND NOT A DERIVED ONE. The existing /v/{sid} links re-encode
-- the video's UUID as base58 (internal/shortid) — free, but 16-22 characters and
-- reversible, so the "short" link still carries the full primary key. A stored
-- code is opaque and fixed-length: it says nothing about the row it names, and
-- it is the shape people recognise from other video platforms.
--
-- WHY POSTGRES MINTS IT AND NOT GO. During a rolling deploy the PREVIOUS release
-- is still inserting videos, and its INSERT lists columns explicitly (sqlc emits
-- no SELECT */RETURNING *), so it cannot know about this one. A DEFAULT is the
-- only mechanism that covers those rows. Minting in Go instead would force a
-- nullable column, a backfill worker, and a second URL form for rows that have
-- no code yet — three moving parts to replace one DEFAULT. This is also exactly
-- what 0006 already does for the primary key itself: the database mints the
-- public identifier (`id UUID ... DEFAULT uuid_generate_v4()`).
--
-- WHY `NOT NULL DEFAULT` IN ONE STATEMENT. The usual add-nullable → backfill →
-- SET NOT NULL sequence is unavailable: scripts/migrate-lint.sh bans SET NOT
-- NULL in up migrations. It is also unnecessary. Because the default expression
-- is VOLATILE, ADD COLUMN rewrites the table and evaluates the function once per
-- row, so every existing video is backfilled with a DISTINCT code by this single
-- statement — no UPDATE pass, and no window in which a video has no code.
-- Measured on postgres:18-alpine: 0.6s for 13.5k rows, 2.6s for 135k (roughly
-- 19us/row). That is an ACCESS EXCLUSIVE lock for the duration, which is why the
-- number is stated here rather than discovered on an operator's instance.
--
-- WHY gen_random_uuid() AS THE ENTROPY SOURCE. It is backed by pg_strong_random,
-- a CSPRNG. random() is not, and must never be used here: for an UNLISTED video
-- the code IS the secret protecting it. 122 random bits reduced mod 58^11 leaves
-- a bias below 2^-57, which is not a meaningful attack surface.
--
-- WHY THERE IS NO COLLISION RETRY LOOP. 58^11 is about 2^64.5. At 13.5k videos a
-- fresh code collides with probability ~5e-16, and the whole 13.5k-row backfill
-- collides with probability ~4e-12. The unique index below is what makes that
-- survivable: it turns the once-in-geological-time event into ONE failed insert
-- the caller retries, instead of two videos sharing a URL. A generate-and-retry
-- loop would be a new pattern in this codebase (every other random token here is
-- 128-256 bits, where collision is simply unmodelled) guarding an event rarer
-- than a bit flip in the retry counter.
--
-- The CHECK is the contract the rest of the system codes against: exactly 11
-- characters of the Bitcoin base58 alphabet, the same alphabet internal/shortid
-- uses (no 0/O/I/l, so a code survives being read aloud or typed by hand).
CREATE FUNCTION vidra_short_code() RETURNS text
LANGUAGE plpgsql VOLATILE AS $fn$
DECLARE
  alphabet CONSTANT text := '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz';
  n numeric := 0;
  h text;
  code text := '';
  i int;
BEGIN
  -- 32 hex digits of CSPRNG output folded into one integer, then taken 11 base58
  -- digits at a time. A leading '1' is a legitimate digit here, not padding:
  -- the code is opaque, so unlike shortid's derived form it encodes no length.
  h := replace(gen_random_uuid()::text, '-', '');
  FOR i IN 1..32 LOOP
    n := n * 16 + ('x' || substr(h, i, 1))::bit(4)::int;
  END LOOP;
  FOR i IN 1..11 LOOP
    code := substr(alphabet, (n % 58)::int + 1, 1) || code;
    n := div(n, 58);
  END LOOP;
  RETURN code;
END;
$fn$;

ALTER TABLE videos ADD COLUMN short_code TEXT NOT NULL DEFAULT vidra_short_code()
    CHECK (short_code ~ '^[1-9A-HJ-NP-Za-km-z]{11}$');

-- Not partial: every video has a code, and the uniqueness is the whole point —
-- it is what makes /v/{code} resolve to exactly one video.
CREATE UNIQUE INDEX videos_short_code_key ON videos (short_code);
