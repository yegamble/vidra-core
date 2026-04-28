-- +goose Up
-- +goose StatementBegin

-- Phase 9 Inner Circle — add tier-gate column to videos.
-- Separated from migration 100 so the Down can refuse to drop when data is present
-- (preventing silent loss of paid-tier configuration on rollback).
-- The streaming-route middleware (T2) reads this column to enforce the gate on
-- master.m3u8 / segment URLs, not just the JSON.

ALTER TABLE videos
    ADD COLUMN inner_circle_tier VARCHAR(16) NULL
    CHECK (inner_circle_tier IS NULL OR inner_circle_tier IN ('supporter','vip','elite'));

CREATE INDEX idx_videos_inner_circle_tier
    ON videos(inner_circle_tier)
    WHERE inner_circle_tier IS NOT NULL;

COMMENT ON COLUMN videos.inner_circle_tier IS 'When non-null, video is gated to Inner Circle members of this tier or higher (elite > vip > supporter). Enforced by RequireInnerCircleAccess middleware on streaming routes.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Refuse to drop the column when any video has it set — prevents silent data loss.
-- Operators must export / migrate / null out values first.
DO $$
DECLARE
    set_count INTEGER;
BEGIN
    SELECT COUNT(*) INTO set_count FROM videos WHERE inner_circle_tier IS NOT NULL;
    IF set_count > 0 THEN
        RAISE EXCEPTION 'Refusing to drop videos.inner_circle_tier — % rows still set; export or null out first.', set_count;
    END IF;
END $$;

DROP INDEX IF EXISTS idx_videos_inner_circle_tier;
ALTER TABLE videos DROP COLUMN IF EXISTS inner_circle_tier;

-- +goose StatementEnd
