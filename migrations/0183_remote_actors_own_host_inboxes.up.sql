-- 0183: cached remote actors keep only inboxes on their own host.
--
-- An actor document names its own inbox and shared inbox, and every Accept,
-- Reject, follow-back and channel fan-out this instance signs is POSTed there.
-- Until this release the fetch stored whatever the document said, so an actor
-- on one server could name another server's inbox and have this instance send
-- signed activities to a host that never federated with it. The fetch now
-- drops a cross-host inbox before caching (federation.cacheFetchedActor);
-- this clears rows cached before it, which are otherwise only rewritten when
-- the actor's key rotates. An emptied inbox reads as "no inbox" to every
-- delivery path. instance_url_host is migration 0166's host parser, the one
-- the instance fan-out already compares with.
CREATE FUNCTION clear_cross_host_remote_inboxes() RETURNS INTEGER
LANGUAGE plpgsql AS $$
DECLARE
    n INTEGER;
BEGIN
    UPDATE remote_actors
    SET inbox_url = CASE
            WHEN inbox_url <> '' AND instance_url_host(inbox_url) IS DISTINCT FROM instance_url_host(actor_url)
            THEN '' ELSE inbox_url END,
        shared_inbox_url = CASE
            WHEN COALESCE(shared_inbox_url, '') <> '' AND instance_url_host(shared_inbox_url) IS DISTINCT FROM instance_url_host(actor_url)
            THEN NULL ELSE shared_inbox_url END
    WHERE (inbox_url <> '' AND instance_url_host(inbox_url) IS DISTINCT FROM instance_url_host(actor_url))
       OR (COALESCE(shared_inbox_url, '') <> '' AND instance_url_host(shared_inbox_url) IS DISTINCT FROM instance_url_host(actor_url));
    GET DIAGNOSTICS n = ROW_COUNT;
    RETURN n;
END;
$$;

SELECT clear_cross_host_remote_inboxes();
