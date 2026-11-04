-- name: ReadCatalogBootstrapState :one
-- Read under brief source authority. Original predecessors survive progress;
-- a used epoch that is not current is retired, never a reusable bootstrap ID.
SELECT s.epoch AS current_epoch,e.epoch AS requested_epoch,
       e.source_predecessor,e.receiver_predecessor
FROM (SELECT 1) anchor
LEFT JOIN search_catalog_state s ON s.scope='local'
LEFT JOIN search_catalog_epochs e ON e.scope='local' AND e.epoch=$1::uuid;

-- name: InsertCatalogEpoch :exec
INSERT INTO search_catalog_epochs(scope,epoch,source_predecessor,receiver_predecessor)
VALUES ('local',sqlc.arg(epoch)::uuid,sqlc.narg(source_predecessor)::uuid,sqlc.narg(receiver_predecessor)::uuid);

-- name: DeleteCatalogCurrentState :execrows
-- Reset mutable authority/payload only. Permanent history and pre-bootstrap
-- transactional dirty hints deliberately outlive an accepted epoch transition.
DELETE FROM search_catalog_state WHERE scope='local' AND epoch=$1::uuid;

-- name: InitializeCatalogState :exec
-- Defaults require a fresh full; this row alone grants no read authority.
INSERT INTO search_catalog_state(scope,epoch) VALUES ('local',$1::uuid);
