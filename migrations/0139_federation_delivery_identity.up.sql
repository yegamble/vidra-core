-- 0139: an outbound federation delivery remembers which request produced it.
--
-- A17 recorded it as a gap and A29 confirmed it from the federation side:
-- federation_deliveries write no job_runs identity at all, so the operational
-- run for a delivery carries an empty request_id/correlation_id and the admin's
-- own advice — "inspect correlated system logs" — points at logs that share no
-- id with the run. A publish that fans out to twelve inboxes produces twelve
-- deliveries and no way to tell they came from one act.
--
-- The columns live on the QUEUE ROW rather than only on the projected job_runs
-- row, because the queue row is what survives: the drain reads it, a dead letter
-- keeps it, and an operator debugging a stuck inbox is looking at
-- federation_deliveries, not at a projection of it.
ALTER TABLE federation_deliveries
    ADD COLUMN request_id     TEXT NOT NULL DEFAULT '' CHECK (length(request_id) <= 128),
    ADD COLUMN correlation_id TEXT NOT NULL DEFAULT '' CHECK (length(correlation_id) <= 128);

-- Carry that identity onto the job_runs row the 0083 projection creates.
--
-- A SECOND TRIGGER rather than a rewrite of sync_legacy_job_run(): that function
-- is shared by seven legacy queues, and replacing 150 lines of it to add two
-- assignments would fork a body that must stay identical for every other table.
-- Row triggers fire in alphabetical order by trigger NAME, and
-- "federation_deliveries_operational_projection" sorts before
-- "federation_deliveries_run_identity", so the projection has already inserted
-- or updated the run by the time this runs. The guard on the existing value
-- makes it idempotent and keeps a later empty-identity update from erasing an
-- id the enqueue recorded.
CREATE OR REPLACE FUNCTION federation_delivery_run_identity() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.request_id = '' AND NEW.correlation_id = '' THEN
        RETURN NEW;
    END IF;
    UPDATE job_runs
       SET request_id     = CASE WHEN job_runs.request_id = '' THEN NEW.request_id ELSE job_runs.request_id END,
           correlation_id = CASE WHEN job_runs.correlation_id = '' THEN NEW.correlation_id ELSE job_runs.correlation_id END
     WHERE job_runs.queue = 'federation_deliveries'
       AND job_runs.source_id = NEW.id::text;
    RETURN NEW;
END;
$$;

CREATE TRIGGER federation_deliveries_run_identity
AFTER INSERT OR UPDATE ON federation_deliveries
FOR EACH ROW EXECUTE FUNCTION federation_delivery_run_identity();
