-- Down 0079: drop the resolver + stage columns added for W2.C1.
ALTER TABLE import_jobs
    DROP COLUMN IF EXISTS stage,
    DROP COLUMN IF EXISTS resolver;
