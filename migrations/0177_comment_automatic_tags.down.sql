-- Revert 0177. Tags are derived data, so dropping them loses nothing that a
-- later re-apply plus an edit cannot recompute.
DROP TABLE IF EXISTS comment_automatic_tags;
