-- PeerTube can publish an upload months after it was created. Both times
-- survive migration, as does the independent date of first publication elsewhere.
ALTER TABLE videos ADD COLUMN peertube_published_at timestamptz;
