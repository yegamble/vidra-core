-- Create comments table for video comments with threading support
CREATE TABLE IF NOT EXISTS comments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_id UUID REFERENCES comments(id) ON DELETE CASCADE,
    body TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 10000),
    status VARCHAR(50) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deleted', 'flagged', 'hidden')),
    flag_count INTEGER NOT NULL DEFAULT 0,
    edited_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Create indexes for performance
CREATE INDEX idx_comments_video_id ON comments(video_id) WHERE status = 'active';
CREATE INDEX idx_comments_user_id ON comments(user_id);
CREATE INDEX idx_comments_parent_id ON comments(parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX idx_comments_created_at ON comments(created_at DESC);
CREATE INDEX idx_comments_status ON comments(status) WHERE status != 'active';

-- Create comment_flags table for tracking user flags
CREATE TABLE IF NOT EXISTS comment_flags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    comment_id UUID NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason VARCHAR(100) NOT NULL CHECK (reason IN ('spam', 'harassment', 'hate_speech', 'inappropriate', 'misinformation', 'other')),
    details TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(comment_id, user_id) -- One flag per user per comment
);

CREATE INDEX idx_comment_flags_comment_id ON comment_flags(comment_id);
CREATE INDEX idx_comment_flags_user_id ON comment_flags(user_id);

-- Update comments updated_at timestamp automatically
CREATE OR REPLACE FUNCTION update_comments_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_comments_updated_at
    BEFORE UPDATE ON comments
    FOR EACH ROW
    EXECUTE FUNCTION update_comments_updated_at();

-- Function to increment flag count when a new flag is added
CREATE OR REPLACE FUNCTION increment_comment_flag_count()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE comments
    SET flag_count = flag_count + 1
    WHERE id = NEW.comment_id;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_increment_flag_count
    AFTER INSERT ON comment_flags
    FOR EACH ROW
    EXECUTE FUNCTION increment_comment_flag_count();

-- Function to decrement flag count when a flag is removed
CREATE OR REPLACE FUNCTION decrement_comment_flag_count()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE comments
    SET flag_count = flag_count - 1
    WHERE id = OLD.comment_id AND flag_count > 0;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_decrement_flag_count
    AFTER DELETE ON comment_flags
    FOR EACH ROW
    EXECUTE FUNCTION decrement_comment_flag_count();

-- Add notification trigger for new comments
CREATE OR REPLACE FUNCTION notify_on_new_comment()
RETURNS TRIGGER AS $$
DECLARE
    video_owner_id UUID;
    parent_comment_user_id UUID;
BEGIN
    -- Get video owner
    SELECT u.id INTO video_owner_id
    FROM videos v
    JOIN channels c ON v.channel_id = c.id
    JOIN users u ON c.account_id = u.id
    WHERE v.id = NEW.video_id;

    -- Notify video owner if they're not the commenter
    IF video_owner_id IS NOT NULL AND video_owner_id != NEW.user_id THEN
        INSERT INTO notifications (user_id, type, data, created_at)
        VALUES (
            video_owner_id,
            'comment',
            jsonb_build_object(
                'comment_id', NEW.id,
                'video_id', NEW.video_id,
                'user_id', NEW.user_id,
                'body', LEFT(NEW.body, 100)
            ),
            NOW()
        );
    END IF;

    -- If this is a reply, notify the parent comment author
    IF NEW.parent_id IS NOT NULL THEN
        SELECT user_id INTO parent_comment_user_id
        FROM comments
        WHERE id = NEW.parent_id;

        IF parent_comment_user_id IS NOT NULL AND parent_comment_user_id != NEW.user_id THEN
            INSERT INTO notifications (user_id, type, data, created_at)
            VALUES (
                parent_comment_user_id,
                'comment_reply',
                jsonb_build_object(
                    'comment_id', NEW.id,
                    'parent_id', NEW.parent_id,
                    'video_id', NEW.video_id,
                    'user_id', NEW.user_id,
                    'body', LEFT(NEW.body, 100)
                ),
                NOW()
            );
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_notify_on_comment
    AFTER INSERT ON comments
    FOR EACH ROW
    WHEN (NEW.status = 'active')
    EXECUTE FUNCTION notify_on_new_comment();
