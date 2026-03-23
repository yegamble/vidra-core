-- Create encoding_jobs table for video processing queue
CREATE TABLE IF NOT EXISTS encoding_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    source_file_path TEXT NOT NULL,
    source_resolution VARCHAR(10) NOT NULL,
    target_resolutions TEXT[] NOT NULL DEFAULT '{}',
    status VARCHAR(20) NOT NULL CHECK (status IN ('pending','processing','completed','failed')) DEFAULT 'pending',
    progress INTEGER NOT NULL DEFAULT 0 CHECK (progress >= 0 AND progress <= 100),
    error_message TEXT,
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Indexes for efficient querying and job processing
CREATE INDEX IF NOT EXISTS idx_encoding_jobs_video_id ON encoding_jobs(video_id);
CREATE INDEX IF NOT EXISTS idx_encoding_jobs_status ON encoding_jobs(status);
CREATE INDEX IF NOT EXISTS idx_encoding_jobs_created_at ON encoding_jobs(created_at);
CREATE INDEX IF NOT EXISTS idx_encoding_jobs_status_created ON encoding_jobs(status, created_at);

-- Trigger to update updated_at
DROP TRIGGER IF EXISTS update_encoding_jobs_updated_at ON encoding_jobs;
CREATE TRIGGER update_encoding_jobs_updated_at
    BEFORE UPDATE ON encoding_jobs
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();
