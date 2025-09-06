-- Create video_categories table
CREATE TABLE IF NOT EXISTS video_categories (
    id UUID DEFAULT gen_random_uuid() PRIMARY KEY,
    name VARCHAR(100) NOT NULL UNIQUE,
    slug VARCHAR(100) NOT NULL UNIQUE,
    description TEXT,
    icon VARCHAR(50), -- For storing icon class names or emoji
    color VARCHAR(7), -- Hex color code for UI display
    display_order INTEGER DEFAULT 0,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL
);

-- Create indexes for performance
CREATE INDEX idx_video_categories_slug ON video_categories(slug);
CREATE INDEX idx_video_categories_is_active ON video_categories(is_active);
CREATE INDEX idx_video_categories_display_order ON video_categories(display_order);

-- Insert default categories
INSERT INTO video_categories (name, slug, description, icon, color, display_order, is_active) VALUES
    ('Music', 'music', 'Music videos, concerts, and audio content', '🎵', '#FF0000', 1, true),
    ('Gaming', 'gaming', 'Gaming videos, walkthroughs, and streams', '🎮', '#00FF00', 2, true),
    ('Education', 'education', 'Educational content and tutorials', '📚', '#0066CC', 3, true),
    ('Entertainment', 'entertainment', 'Entertainment and comedy content', '🎭', '#FF9900', 4, true),
    ('News & Politics', 'news-politics', 'News and political content', '📰', '#666666', 5, true),
    ('Science & Technology', 'science-technology', 'Science and technology content', '🔬', '#00CCFF', 6, true),
    ('Sports', 'sports', 'Sports and fitness content', '⚽', '#009900', 7, true),
    ('Travel & Events', 'travel-events', 'Travel vlogs and event coverage', '✈️', '#FF6600', 8, true),
    ('Film & Animation', 'film-animation', 'Movies, animations, and short films', '🎬', '#CC00CC', 9, true),
    ('People & Blogs', 'people-blogs', 'Personal vlogs and lifestyle content', '👥', '#FF3366', 10, true),
    ('Pets & Animals', 'pets-animals', 'Pet and animal videos', '🐾', '#996633', 11, true),
    ('How-to & Style', 'howto-style', 'DIY, fashion, and style guides', '💄', '#FF99CC', 12, true),
    ('Autos & Vehicles', 'autos-vehicles', 'Automotive and vehicle content', '🚗', '#333333', 13, true),
    ('Nonprofits & Activism', 'nonprofits-activism', 'Nonprofit and activism content', '🤝', '#339933', 14, true),
    ('Other', 'other', 'Other content', '📁', '#999999', 999, true);

-- Add trigger to update updated_at
CREATE OR REPLACE FUNCTION update_video_categories_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_update_video_categories_updated_at
    BEFORE UPDATE ON video_categories
    FOR EACH ROW
    EXECUTE FUNCTION update_video_categories_updated_at();