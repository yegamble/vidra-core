-- Drop invalid partial index if it exists and recreate a safe index
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM   pg_class c
        JOIN   pg_namespace n ON n.oid = c.relnamespace
        WHERE  c.relkind = 'i'
        AND    c.relname = 'idx_sessions_active'
    ) THEN
        DROP INDEX idx_sessions_active;
    END IF;
END$$;

CREATE INDEX IF NOT EXISTS idx_sessions_active ON sessions(user_id, expires_at);
