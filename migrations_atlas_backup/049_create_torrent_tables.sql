-- Migration: Create torrent support tables
-- Description: Adds tables for BitTorrent/WebTorrent P2P video distribution
-- Sprint: 8 - Torrent Support with IPFS Hybrid

-- Table: video_torrents
-- Stores torrent metadata for each video
CREATE TABLE IF NOT EXISTS video_torrents (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    info_hash TEXT NOT NULL UNIQUE,
    torrent_file_path TEXT NOT NULL,
    magnet_uri TEXT NOT NULL,
    piece_length INTEGER NOT NULL DEFAULT 262144, -- 256KB default piece size
    total_size_bytes BIGINT NOT NULL,
    seeders INTEGER DEFAULT 0,
    leechers INTEGER DEFAULT 0,
    completed_downloads INTEGER DEFAULT 0,
    is_seeding BOOLEAN DEFAULT false,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_video_torrent UNIQUE(video_id)
);

-- Table: torrent_trackers
-- Stores tracker URLs with WebSocket support for WebTorrent
CREATE TABLE IF NOT EXISTS torrent_trackers (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    announce_url TEXT NOT NULL UNIQUE,
    is_websocket BOOLEAN DEFAULT false,
    is_active BOOLEAN DEFAULT true,
    priority INTEGER DEFAULT 0,
    success_count INTEGER DEFAULT 0,
    failure_count INTEGER DEFAULT 0,
    last_checked_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Table: torrent_peers
-- Tracks active peers for each torrent
CREATE TABLE IF NOT EXISTS torrent_peers (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    info_hash TEXT NOT NULL,
    peer_id TEXT NOT NULL,
    ip_address INET NOT NULL,
    port INTEGER NOT NULL,
    uploaded_bytes BIGINT DEFAULT 0,
    downloaded_bytes BIGINT DEFAULT 0,
    left_bytes BIGINT DEFAULT 0,
    event TEXT CHECK (event IN ('started', 'stopped', 'completed', NULL)),
    user_agent TEXT,
    supports_webtorrent BOOLEAN DEFAULT false,
    last_announce_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_peer UNIQUE(info_hash, peer_id)
);

-- Table: torrent_web_seeds
-- HTTP/HTTPS URLs that can serve as web seeds
CREATE TABLE IF NOT EXISTS torrent_web_seeds (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_torrent_id UUID NOT NULL REFERENCES video_torrents(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    is_active BOOLEAN DEFAULT true,
    priority INTEGER DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_web_seed UNIQUE(video_torrent_id, url)
);

-- Table: torrent_stats
-- Hourly statistics for torrent distribution
CREATE TABLE IF NOT EXISTS torrent_stats (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_torrent_id UUID NOT NULL REFERENCES video_torrents(id) ON DELETE CASCADE,
    hour TIMESTAMP NOT NULL,
    total_peers INTEGER DEFAULT 0,
    total_seeds INTEGER DEFAULT 0,
    bytes_uploaded BIGINT DEFAULT 0,
    bytes_downloaded BIGINT DEFAULT 0,
    completed_downloads INTEGER DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_hourly_stats UNIQUE(video_torrent_id, hour)
);

-- Indexes for performance
CREATE INDEX idx_video_torrents_video_id ON video_torrents(video_id);
CREATE INDEX idx_video_torrents_info_hash ON video_torrents(info_hash);
CREATE INDEX idx_video_torrents_is_seeding ON video_torrents(is_seeding);
CREATE INDEX idx_torrent_peers_info_hash ON torrent_peers(info_hash);
CREATE INDEX idx_torrent_peers_last_announce ON torrent_peers(last_announce_at);
CREATE INDEX idx_torrent_web_seeds_torrent_id ON torrent_web_seeds(video_torrent_id);
CREATE INDEX idx_torrent_stats_torrent_id_hour ON torrent_stats(video_torrent_id, hour DESC);

-- Insert default WebTorrent-compatible trackers
INSERT INTO torrent_trackers (announce_url, is_websocket, priority, is_active) VALUES
    ('wss://tracker.openwebtorrent.com', true, 1, true),
    ('wss://tracker.btorrent.xyz', true, 2, true),
    ('wss://tracker.fastcast.nz', true, 3, true),
    ('wss://tracker.webtorrent.dev', true, 4, true)
ON CONFLICT (announce_url) DO NOTHING;

-- Function: Clean up old peer announcements (>1 hour)
CREATE OR REPLACE FUNCTION cleanup_old_torrent_peers()
RETURNS void AS $$
BEGIN
    DELETE FROM torrent_peers
    WHERE last_announce_at < NOW() - INTERVAL '1 hour';
END;
$$ LANGUAGE plpgsql;

-- Function: Update torrent seeders/leechers count
CREATE OR REPLACE FUNCTION update_torrent_peer_counts()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE video_torrents
    SET seeders = (
            SELECT COUNT(DISTINCT peer_id)
            FROM torrent_peers
            WHERE info_hash = NEW.info_hash
            AND left_bytes = 0
            AND last_announce_at > NOW() - INTERVAL '30 minutes'
        ),
        leechers = (
            SELECT COUNT(DISTINCT peer_id)
            FROM torrent_peers
            WHERE info_hash = NEW.info_hash
            AND left_bytes > 0
            AND last_announce_at > NOW() - INTERVAL '30 minutes'
        ),
        updated_at = CURRENT_TIMESTAMP
    WHERE info_hash = NEW.info_hash;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Trigger: Update peer counts on peer changes
CREATE TRIGGER trigger_update_peer_counts
    AFTER INSERT OR UPDATE OR DELETE ON torrent_peers
    FOR EACH ROW
    EXECUTE FUNCTION update_torrent_peer_counts();

-- Function: Record torrent completion
CREATE OR REPLACE FUNCTION record_torrent_completion(
    p_info_hash TEXT,
    p_peer_id TEXT
)
RETURNS void AS $$
BEGIN
    -- Update completed downloads counter
    UPDATE video_torrents
    SET completed_downloads = completed_downloads + 1,
        updated_at = CURRENT_TIMESTAMP
    WHERE info_hash = p_info_hash;

    -- Update peer status
    UPDATE torrent_peers
    SET event = 'completed',
        left_bytes = 0,
        last_announce_at = CURRENT_TIMESTAMP
    WHERE info_hash = p_info_hash AND peer_id = p_peer_id;
END;
$$ LANGUAGE plpgsql;

-- Function: Get torrent health (ratio of seeders to leechers)
CREATE OR REPLACE FUNCTION get_torrent_health(p_video_id UUID)
RETURNS TABLE(
    seeders INTEGER,
    leechers INTEGER,
    health_ratio DECIMAL,
    is_healthy BOOLEAN
) AS $$
BEGIN
    RETURN QUERY
    SELECT
        vt.seeders,
        vt.leechers,
        CASE
            WHEN vt.leechers = 0 THEN 999.99
            ELSE ROUND(vt.seeders::DECIMAL / vt.leechers::DECIMAL, 2)
        END as health_ratio,
        vt.seeders >= 3 as is_healthy
    FROM video_torrents vt
    WHERE vt.video_id = p_video_id;
END;
$$ LANGUAGE plpgsql;

-- Comments for documentation
COMMENT ON TABLE video_torrents IS 'Stores BitTorrent metadata for P2P video distribution';
COMMENT ON TABLE torrent_trackers IS 'WebTorrent-compatible tracker URLs';
COMMENT ON TABLE torrent_peers IS 'Active peers participating in torrent swarms';
COMMENT ON TABLE torrent_web_seeds IS 'HTTP fallback URLs for hybrid distribution';
COMMENT ON TABLE torrent_stats IS 'Hourly statistics for monitoring P2P distribution';

COMMENT ON COLUMN video_torrents.info_hash IS 'SHA1 hash of torrent info dictionary';
COMMENT ON COLUMN video_torrents.piece_length IS 'Size of each piece in bytes (256KB for WebTorrent compatibility)';
COMMENT ON COLUMN torrent_peers.supports_webtorrent IS 'Whether peer supports WebRTC for browser P2P';
COMMENT ON COLUMN torrent_trackers.is_websocket IS 'Whether tracker uses WebSocket protocol (required for WebTorrent)';
