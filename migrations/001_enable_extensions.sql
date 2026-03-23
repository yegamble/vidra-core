-- +goose Up
-- +goose StatementBegin
-- Enable required PostgreSQL extensions for Athena
-- Keep in sync with init-* SQLs

-- UUID helpers and gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Search and indexing helpers
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS btree_gin;

-- Create update trigger function (used across many tables)
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety
