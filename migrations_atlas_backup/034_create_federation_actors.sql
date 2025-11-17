-- Dedicated table for ATProto ingestion actors and scheduling

CREATE TABLE IF NOT EXISTS federation_actors (
    actor TEXT PRIMARY KEY,                 -- handle or DID
    enabled BOOLEAN NOT NULL DEFAULT true,
    cursor TEXT,
    next_at TIMESTAMP,
    attempts INTEGER NOT NULL DEFAULT 0,
    rate_limit_seconds INTEGER NOT NULL DEFAULT 60,
    last_error TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_fed_actors_next ON federation_actors(next_at);

CREATE TRIGGER update_fed_actors_updated_at BEFORE UPDATE ON federation_actors
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

