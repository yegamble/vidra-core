DROP TRIGGER IF EXISTS storage_migrations_operational_projection ON storage_migrations;
DROP FUNCTION IF EXISTS sync_storage_migration_job_run();
DROP TABLE IF EXISTS storage_migration_objects;
DROP TABLE IF EXISTS storage_migrations;
DELETE FROM job_events WHERE job_id IN (SELECT id FROM job_runs WHERE queue = 'storage_migrations');
DELETE FROM job_runs WHERE queue = 'storage_migrations';
