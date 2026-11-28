DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM retained_upload_recoveries r JOIN upload_finalize_jobs j ON j.id=r.job_id
               WHERE j.state IN ('pending', 'running')) THEN
        RAISE EXCEPTION 'finish or cancel retained upload recovery jobs before rolling back';
    END IF;
END $$;
DROP VIEW current_retained_scan_recoveries;
DROP VIEW retained_scan_sources;
DROP TABLE retained_upload_recoveries;
