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

-- name: ListStreamingPlaylistRefs :many
-- video id + master key of every recorded streaming playlist, so the GC can
-- tell which HLS GENERATION (streaming-playlists/<id>/rN/ vs the legacy
-- in-place layout) is the live one after a source replacement (config-parity
-- W14). Failed playlists carry master_key '' and yield no live generation.
SELECT video_id, master_key FROM streaming_playlists;

-- name: CountForeignLayoutMediaRefs :one
-- How many media references this install holds that point at object keys IT DID
-- NOT LAY OUT — the evidence that the object store is SHARED with another system
-- rather than merely inherited from a previous install of this one. A
-- reference-mode PeerTube import (docs/peertube-migration.md §4) is the
-- documented way to get here: it points STORAGE_* at the SOURCE instance's own
-- bucket and records that instance's keys verbatim, so adopting the bucket would
-- arm destructive GC against media a live PeerTube is still serving.
--
-- Both halves are shape tests on keys the database itself holds, which is what
-- makes the answer durable: Vidra stores an original at web-videos/<video_id>…
-- and an HLS master under streaming-playlists/<video_id>/, always. An empty
-- video_files.sha256 marks a reference-mode row too, but only until the
-- content-hash backfill fills it in — a signal that decays into a false negative
-- is the one thing a safety gate must not be built on.
SELECT (
    (SELECT count(*) FROM video_files
      WHERE storage_key LIKE 'web-videos/%'
        AND storage_key NOT LIKE 'web-videos/' || video_id::text || '%')
  + (SELECT count(*) FROM streaming_playlists
      WHERE master_key <> ''
        AND master_key NOT LIKE 'streaming-playlists/' || video_id::text || '/%')
)::bigint AS foreign_refs;

-- name: ListPlaylistThumbnailRefs :many
-- id + extension of every playlist that has an uploaded cover, so its
-- playlist-thumbnails/<id>.<ext> key can be reconstructed as a live reference.
SELECT id, thumbnail_ext FROM playlists WHERE thumbnail_ext IS NOT NULL;
