-- name: ListPublicIPFSRootPins :many
-- A CID shared by several objects stays available when any current public
-- reference is eligible. Return one extra row so the caller detects truncation.
SELECT * FROM media_ipfs_pins
WHERE cid = $1 AND network = 'public' AND state = 'pinned'
ORDER BY object_key LIMIT 129;
