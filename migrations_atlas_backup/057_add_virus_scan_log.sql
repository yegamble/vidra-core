-- Create table to log virus scan results
CREATE TABLE IF NOT EXISTS virus_scan_log (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    file_path TEXT NOT NULL,
    file_size BIGINT NOT NULL,
    file_hash TEXT, -- SHA256
    scan_result TEXT NOT NULL CHECK (scan_result IN ('clean', 'infected', 'error', 'warning')),
    virus_name TEXT,
    quarantined BOOLEAN DEFAULT FALSE,
    quarantine_path TEXT,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    upload_session_id TEXT,
    scan_duration_ms INTEGER,
    scanned_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    metadata JSONB
);

-- Create indexes for common queries
CREATE INDEX idx_virus_scan_log_result ON virus_scan_log(scan_result);
CREATE INDEX idx_virus_scan_log_user ON virus_scan_log(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX idx_virus_scan_log_scanned_at ON virus_scan_log(scanned_at);
CREATE INDEX idx_virus_scan_log_quarantined ON virus_scan_log(quarantined) WHERE quarantined = TRUE;

-- Add comment to table
COMMENT ON TABLE virus_scan_log IS 'Audit log for virus scanning operations';
COMMENT ON COLUMN virus_scan_log.scan_result IS 'Result of virus scan: clean, infected, error, or warning';
COMMENT ON COLUMN virus_scan_log.virus_name IS 'Name of detected virus/malware if infected';
COMMENT ON COLUMN virus_scan_log.quarantined IS 'Whether file was quarantined';
COMMENT ON COLUMN virus_scan_log.metadata IS 'Additional metadata about the scan (JSON)';
