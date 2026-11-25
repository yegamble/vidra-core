-- The recent public feed can stop after the requested page even for imports
-- published long after their upload. UUID breaks ties across pages.
CREATE INDEX videos_publication_order_idx
    ON videos (COALESCE(peertube_published_at, created_at) DESC, id DESC)
    WHERE privacy = 'public' AND state = 'published';
