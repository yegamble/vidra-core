-- +goose Up
-- +goose StatementBegin
-- Create video_ratings table for like/dislike functionality
CREATE TABLE IF NOT EXISTS video_ratings (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    rating INTEGER NOT NULL CHECK (rating IN (-1, 0, 1)), -- -1: dislike, 0: none/neutral, 1: like
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, video_id)
);

-- Create indexes for video_ratings
CREATE INDEX idx_video_ratings_video_id ON video_ratings(video_id);
CREATE INDEX idx_video_ratings_user_id ON video_ratings(user_id);
CREATE INDEX idx_video_ratings_rating ON video_ratings(video_id, rating) WHERE rating != 0;

-- Add rating aggregates to videos table
ALTER TABLE videos
ADD COLUMN IF NOT EXISTS likes_count INTEGER NOT NULL DEFAULT 0,
ADD COLUMN IF NOT EXISTS dislikes_count INTEGER NOT NULL DEFAULT 0;

-- Create playlists table
CREATE TABLE IF NOT EXISTS playlists (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL CHECK (char_length(name) BETWEEN 1 AND 255),
    description TEXT,
    privacy VARCHAR(50) NOT NULL DEFAULT 'private' CHECK (privacy IN ('public', 'unlisted', 'private')),
    thumbnail_url TEXT,
    is_watch_later BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Create indexes for playlists
CREATE INDEX idx_playlists_user_id ON playlists(user_id);
CREATE INDEX idx_playlists_privacy ON playlists(privacy) WHERE privacy = 'public';
CREATE INDEX idx_playlists_created_at ON playlists(created_at DESC);

-- Ensure only one watch_later playlist per user
CREATE UNIQUE INDEX idx_playlists_watch_later_per_user
ON playlists(user_id)
WHERE is_watch_later = TRUE;

-- Create playlist_items table
CREATE TABLE IF NOT EXISTS playlist_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    playlist_id UUID NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(playlist_id, video_id),
    UNIQUE(playlist_id, position)
);

-- Create indexes for playlist_items
CREATE INDEX idx_playlist_items_playlist_id ON playlist_items(playlist_id);
CREATE INDEX idx_playlist_items_video_id ON playlist_items(video_id);
CREATE INDEX idx_playlist_items_position ON playlist_items(playlist_id, position);

-- Function to update video rating counts
CREATE OR REPLACE FUNCTION update_video_rating_counts()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' OR TG_OP = 'UPDATE' THEN
        -- Update the counts for the video
        UPDATE videos
        SET
            likes_count = (
                SELECT COUNT(*)
                FROM video_ratings
                WHERE video_id = NEW.video_id AND rating = 1
            ),
            dislikes_count = (
                SELECT COUNT(*)
                FROM video_ratings
                WHERE video_id = NEW.video_id AND rating = -1
            )
        WHERE id = NEW.video_id;

        -- If it's an update and the video changed, update the old video too
        IF TG_OP = 'UPDATE' AND OLD.video_id != NEW.video_id THEN
            UPDATE videos
            SET
                likes_count = (
                    SELECT COUNT(*)
                    FROM video_ratings
                    WHERE video_id = OLD.video_id AND rating = 1
                ),
                dislikes_count = (
                    SELECT COUNT(*)
                    FROM video_ratings
                    WHERE video_id = OLD.video_id AND rating = -1
                )
            WHERE id = OLD.video_id;
        END IF;
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE videos
        SET
            likes_count = (
                SELECT COUNT(*)
                FROM video_ratings
                WHERE video_id = OLD.video_id AND rating = 1
            ),
            dislikes_count = (
                SELECT COUNT(*)
                FROM video_ratings
                WHERE video_id = OLD.video_id AND rating = -1
            )
        WHERE id = OLD.video_id;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger for updating video rating counts
CREATE TRIGGER trigger_update_video_rating_counts
    AFTER INSERT OR UPDATE OR DELETE ON video_ratings
    FOR EACH ROW
    EXECUTE FUNCTION update_video_rating_counts();

-- Function to update playlist updated_at timestamp
CREATE OR REPLACE FUNCTION update_playlist_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger for updating playlist timestamp
CREATE TRIGGER trigger_playlists_updated_at
    BEFORE UPDATE ON playlists
    FOR EACH ROW
    EXECUTE FUNCTION update_playlist_updated_at();

-- Function to maintain playlist item positions
CREATE OR REPLACE FUNCTION maintain_playlist_positions()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        -- If position is null or conflicts, append to end
        IF NEW.position IS NULL THEN
            NEW.position = COALESCE(
                (SELECT MAX(position) + 1 FROM playlist_items WHERE playlist_id = NEW.playlist_id),
                0
            );
        END IF;
    ELSIF TG_OP = 'DELETE' THEN
        -- Shift positions down after deletion
        UPDATE playlist_items
        SET position = position - 1
        WHERE playlist_id = OLD.playlist_id
        AND position > OLD.position;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger for maintaining playlist positions
CREATE TRIGGER trigger_maintain_playlist_positions
    AFTER DELETE ON playlist_items
    FOR EACH ROW
    EXECUTE FUNCTION maintain_playlist_positions();

-- Create a default "Watch Later" playlist for existing users
INSERT INTO playlists (user_id, name, description, privacy, is_watch_later)
SELECT
    id,
    'Watch Later',
    'Videos to watch later',
    'private',
    TRUE
FROM users
WHERE NOT EXISTS (
    SELECT 1 FROM playlists
    WHERE playlists.user_id = users.id
    AND playlists.is_watch_later = TRUE
);
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety
