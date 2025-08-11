-- Enable required PostgreSQL extensions for Athena video platform
-- As specified in CLAUDE.md

-- UUID generation extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Full-text search with trigram matching
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Accent-insensitive text search
CREATE EXTENSION IF NOT EXISTS unaccent;

-- Generalized Inverted Index (GIN) for B-tree operations
CREATE EXTENSION IF NOT EXISTS btree_gin;

-- Create indexes for common search patterns
-- These will be created by migrations, but we ensure extensions are available

-- Log successful initialization
\echo 'PostgreSQL extensions initialized successfully for Athena platform';