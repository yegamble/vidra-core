-- name: ReadCatalogCaptureDemand :one
-- Only polling/coalescing; capture owns exact RR-visible hint acknowledgement.
-- TWIN catalog_legacy.sql and catalog_read_proof.sql static event whitelist.
SELECT EXISTS(SELECT 1 FROM search_catalog_dirty WHERE scope='local') AS dirty,
       EXISTS(SELECT 1 FROM search_outbox WHERE state IN ('pending','dead')
           AND event_type IN ('video.upsert','video.suppress','channel.upsert','channel.delete',
                              'user.suppress','reconcile.begin','reconcile.page','reconcile.end')) AS legacy_outstanding;
