# Asset Storage Layout (PeerTube-aligned)

> Convention for how vidra-core lays out user-generated **assets** on the storage
> backend (`internal/storage`). The goal is **consistency with PeerTube's storage
> structure** so the two are easy to compare and migrate between
> (`.ralph/specs/peertube-import.md`). This governs object *keys*, not code layout.

## Rule: one top-level directory per asset KIND

PeerTube stores each asset kind in its own top-level `storage/<kind>/` directory
(not a per-video directory). vidra mirrors this. Storage keys are opaque,
forward-slash object keys under the configured `STORAGE_LOCAL_ROOT` (default
`./data/media`), so a thumbnail for video `<id>` lives at
`./data/media/thumbnails/<id>.jpg`.

PeerTube buckets (from its `storage:` config) and the vidra mapping:

| PeerTube bucket          | Holds                                   | vidra key format                 | Status |
|--------------------------|-----------------------------------------|----------------------------------|--------|
| `web-videos/`            | the video file served for web playback  | `web-videos/<id><ext>`           | **in use** (the served upload; `video_files.kind='original'`) |
| `thumbnails/`            | poster/thumbnail images                 | `thumbnails/<id>.jpg`            | **in use** (`kind='thumbnail'`) |
| `streaming-playlists/`   | HLS playlists + segments                | `streaming-playlists/<id>/...`   | **in use** (P6 transcoding: `master.m3u8` + `<height>p/playlist.m3u8` + `<height>p/seg_NNNNN.ts`; relative URIs so the API proxies them) |
| `original-video-files/`  | archived original upload (keep-original)| `original-video-files/<id><ext>` | planned (when transcoding + keep-original land) |
| `previews/`              | large preview images                    | `previews/<id>.jpg`             | planned |
| `storyboards/`           | scrubbing storyboards                   | `storyboards/<id>.jpg`          | planned |
| `captions/`              | subtitle/caption files                  | `captions/<id>-<lang>.vtt`      | planned (P13 captions) |
| `avatars/`               | account/channel avatars                 | `avatars/users/<id><ext>`, `avatars/channels/<id><ext>` | **in use** (P5; `user_images`/`channel_images` tables) |
| `banners/`               | account/channel banners (vidra addition — PeerTube keeps banners inside `avatars/`; a separate kind dir follows the one-dir-per-kind rule) | `banners/users/<id><ext>`, `banners/channels/<id><ext>` | **in use** (P5) |
| `exports/`               | account export archives (vidra addition — P4 export; no PeerTube bucket equivalent) | `exports/accounts/<user_id>/<export_id>.json` | **in use** (P4; the `account_exports` table tracks the job + 7-day expiry; the sweeper deletes blob + row) |
| `uploads/`               | resumable-upload chunks in flight (vidra addition — P6.1 chunked upload; no PeerTube bucket equivalent) | `uploads/<session_id>/<n>` | **in use** (P6.1; the `upload_sessions`/`upload_chunks` tables track the session + received-chunk ledger; complete assembles the chunks in order into `web-videos/`, then the chunk prefix is dropped; a 24h sweeper deletes expired/cancelled sessions' chunk prefixes — the failed-upload cleanup) |
| `torrents/`              | .torrent files                          | `torrents/<id>.torrent`         | planned (if/when WebTorrent) |
| `tmp/`                   | scratch during upload/processing        | `tmp/...`                        | planned |

Use these exact directory names when adding a new asset kind. Do **not** invent a
new top-level directory or revert to per-video directories
(`videos/<id>/original.mp4`) — that is the layout we migrated away from.

## Notes / intentional differences from PeerTube

- **Filename is the entity id, not a random uuid.** PeerTube randomizes asset
  filenames because it serves them as static files (so URLs must be unguessable).
  vidra serves assets **through the authenticated API** (`GET /api/v1/videos/:id/original`,
  `.../thumbnail`) and never exposes the storage key, so naming a file by its
  `<id>` is safe and traceable. **If vidra ever serves storage statically, switch
  to random per-file ids** to match PeerTube's unguessability property.
- **`web-videos/` currently holds the unmodified upload.** vidra has no transcoding
  yet, so the served file is the original upload. When the transcode pipeline (P6)
  lands: keep the source in `original-video-files/`, write playable renditions to
  `web-videos/`, and HLS to `streaming-playlists/`. The `video_files.kind` taxonomy
  (`original`/`thumbnail`/…) can be aligned to PeerTube's roles in that same slice.
- `storage_key` stays **opaque to the database** (migration 0008); serving reads the
  stored key, so the scheme can evolve without a schema change. Existing rows keep
  their recorded keys.
- **Avatars/banners keep the upload's extension in the key** (`avatars/users/<id>.png`),
  with the owner type as a subdirectory so user and channel assets of the same kind
  share one top-level dir. Because the key varies with the extension, a re-upload that
  changes type deletes the previously recorded object before storing the new one
  (`internal/profileimage`), so no orphan blob is left behind.
