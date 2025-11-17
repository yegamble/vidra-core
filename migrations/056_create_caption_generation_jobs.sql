-- Migration: Create caption_generation_jobs table
-- Description: Track automatic caption generation jobs for videos using speech-to-text (Whisper)
-- Created: 2025-11-05

-- +goose Up
-- +goose StatementBegin

-- Caption generation jobs table
CREATE TABLE caption_generation_jobs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- Job details
    source_audio_path TEXT NOT NULL, -- Path to audio file extracted from video
    target_language VARCHAR(10), -- Target language code (e.g., 'en', 'es'). NULL = auto-detect
    detected_language VARCHAR(10), -- Language detected by Whisper

    -- Job status
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    progress INTEGER DEFAULT 0 CHECK (progress >= 0 AND progress <= 100),
    error_message TEXT,

    -- Whisper settings
    model_size VARCHAR(20) DEFAULT 'base' CHECK (model_size IN ('tiny', 'base', 'small', 'medium', 'large')),
    provider VARCHAR(20) DEFAULT 'local' CHECK (provider IN ('local', 'openai-api')),

    -- Output
    generated_caption_id UUID REFERENCES captions(id) ON DELETE SET NULL,
    output_format VARCHAR(10) DEFAULT 'vtt' CHECK (output_format IN ('vtt', 'srt')),
    transcription_time_seconds INTEGER, -- Time taken to transcribe

    -- Metadata
    is_automatic BOOLEAN DEFAULT true, -- Auto-triggered after encoding vs manual regeneration
    retry_count INTEGER DEFAULT 0,
    max_retries INTEGER DEFAULT 3,

    -- Timestamps
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Indexes for efficient queries
CREATE INDEX idx_caption_gen_jobs_video_id ON caption_generation_jobs(video_id);
CREATE INDEX idx_caption_gen_jobs_user_id ON caption_generation_jobs(user_id);
CREATE INDEX idx_caption_gen_jobs_status ON caption_generation_jobs(status);
CREATE INDEX idx_caption_gen_jobs_created_at ON caption_generation_jobs(created_at DESC);
CREATE INDEX idx_caption_gen_jobs_status_created ON caption_generation_jobs(status, created_at DESC);

-- Composite index for queue processing (get next pending job)
CREATE INDEX idx_caption_gen_jobs_queue ON caption_generation_jobs(status, created_at ASC)
    WHERE status = 'pending';

-- Trigger to update updated_at timestamp
CREATE TRIGGER update_caption_generation_jobs_updated_at
    BEFORE UPDATE ON caption_generation_jobs
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Add comment for documentation
COMMENT ON TABLE caption_generation_jobs IS 'Job queue for automatic caption generation using Whisper speech-to-text';
COMMENT ON COLUMN caption_generation_jobs.target_language IS 'Explicitly requested language. NULL means auto-detect from audio';
COMMENT ON COLUMN caption_generation_jobs.detected_language IS 'Language detected by Whisper during transcription';
COMMENT ON COLUMN caption_generation_jobs.model_size IS 'Whisper model size: tiny (fastest), base, small, medium, large (most accurate)';
COMMENT ON COLUMN caption_generation_jobs.provider IS 'Transcription provider: local (whisper.cpp) or openai-api';
COMMENT ON COLUMN caption_generation_jobs.is_automatic IS 'True if triggered automatically after encoding; false if manually requested';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS update_caption_generation_jobs_updated_at ON caption_generation_jobs;
DROP INDEX IF EXISTS idx_caption_gen_jobs_queue;
DROP INDEX IF EXISTS idx_caption_gen_jobs_status_created;
DROP INDEX IF EXISTS idx_caption_gen_jobs_created_at;
DROP INDEX IF EXISTS idx_caption_gen_jobs_status;
DROP INDEX IF EXISTS idx_caption_gen_jobs_user_id;
DROP INDEX IF EXISTS idx_caption_gen_jobs_video_id;
DROP TABLE IF EXISTS caption_generation_jobs;

-- +goose StatementEnd
