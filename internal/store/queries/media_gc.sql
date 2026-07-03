-- Media garbage-collection reference queries. The GC sweep lists stored object
-- keys per known prefix and deletes only those with NO database reference. These
-- queries return the full set of keys/ids the database currently references, so
-- an object not in the set is an orphan (see internal/mediagc).

-- name: ListAllVideoFileKeys :many
-- Every stored video-file key (originals, thumbnails, storyboards, webm
-- alternates). Renditions store a key_prefix, not exact keys, so HLS trees are
-- GC'd at the video-id level (ListAllVideoIDs) instead.
SELECT storage_key FROM video_files;

-- name: ListAllCaptionKeys :many
SELECT storage_key FROM captions;

-- name: ListAllVideoIDs :many
-- Every live video id, used to keep the HLS tree (streaming-playlists/<id>/...)
-- of any existing video: a whole tree is orphan only when its video is gone.
SELECT id FROM videos;

-- name: ListPlaylistThumbnailRefs :many
-- id + extension of every playlist that has an uploaded cover, so its
-- playlist-thumbnails/<id>.<ext> key can be reconstructed as a live reference.
SELECT id, thumbnail_ext FROM playlists WHERE thumbnail_ext IS NOT NULL;
