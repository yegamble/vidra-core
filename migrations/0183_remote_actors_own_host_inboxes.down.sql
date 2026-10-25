-- 0183 down: the cleared inboxes are not restored. They pointed off their
-- actor's host, and a re-fetch of the actor rewrites the row from its own
-- document anyway. Only the helper function goes.
DROP FUNCTION IF EXISTS clear_cross_host_remote_inboxes();
