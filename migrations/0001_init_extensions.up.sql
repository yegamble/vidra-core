-- 0001: Enable required PostgreSQL extensions.
-- pg_trgm   -> fuzzy/trigram search for video, channel, and account lookup.
-- uuid-ossp -> server-side UUID generation for public-facing identifiers.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
