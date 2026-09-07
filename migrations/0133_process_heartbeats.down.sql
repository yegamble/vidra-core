DROP TRIGGER IF EXISTS job_events_inherit_identity ON job_events;
DROP FUNCTION IF EXISTS inherit_job_event_identity();
DROP TABLE IF EXISTS process_heartbeats;
