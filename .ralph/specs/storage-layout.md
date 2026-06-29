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
| `streaming-playlists/`   | HLS playlists + segments                | `streaming-playlists/<id>/...`   | planned (P6 transcoding) |
| `original-video-files/`  | archived original upload (keep-original)| `original-video-files/<id><ext>` | planned (when transcoding + keep-original land) |
| `previews/`              | large preview images                    | `previews/<id>.jpg`             | planned |
| `storyboards/`           | scrubbing storyboards                   | `storyboards/<id>.jpg`          | planned |
| `captions/`              | subtitle/caption files                  | `captions/<id>-<lang>.vtt`      | planned (P13 captions) |
| `avatars/`               | account/channel avatars                 | `avatars/<id><ext>`             | planned (P5 avatar upload) |
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
