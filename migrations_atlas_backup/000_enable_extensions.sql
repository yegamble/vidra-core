-- Enable required PostgreSQL extensions for Vidra Core
-- Keep in sync with init-* SQLs

-- UUID helpers and gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Search and indexing helpers
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS btree_gin;
