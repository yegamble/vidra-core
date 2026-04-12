# Post-Upload Video Processing Pipeline Implementation Plan

Created: 2026-04-12
Author: yegamble@gmail.com
Status: PENDING
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

## Summary

**Goal:** Implement a complete post-upload video processing pipeline matching PeerTube's behavior: immediate original file availability (when `waitTranscoding=false`), multi-resolution transcoding with per-resolution incremental availability, thumbnail and storyboard generation, configurable original file cleanup, S3 migration, caption generation, WebTorrent creation, ActivityPub/ATProto/IPFS federation, visibility restrictions for processing videos, and structured completion logging.

**Architecture:** Extends the existing encoding service (`internal/usecase/encoding/service.go`) with a PeerTube-compatible state machine. The upload flow gains a `waitTranscoding` per-video parameter. The encoding pipeline is enhanced to update video state incrementally as each resolution completes, then orchestrates post-processing steps in PeerTube's order. A new `files[]`/`streamingPlaylists[]` API response format provides PeerTube-compatible video file metadata.

**Tech Stack:** Go (Chi router), PostgreSQL (SQLX), existing FFmpeg encoding, existing IPFS/S3/torrent/ATProto/ActivityPub integrations.

## Scope

### In Scope

- Per-video `waitTranscoding` boolean field (PeerTube parity)
- Global `KEEP_ORIGINAL_FILE` config setting
- Original file available for immediate playback when `waitTranscoding=false`
- Per-resolution incremental OutputPaths/S3URLs updates as each resolution finishes
- Original file removal after encoding (when config says remove)
- S3 migration of all resolutions (already partially exists, needs completion hookup)
- WebTorrent generation wired into pipeline (generator exists but not called)
- Thumbnail generation (already exists in pipeline via `generateMediaAssets`, verify ordering)
- Storyboard generation (sprite-sheet thumbnails timeline for seek preview — domain model and API exist, generation logic is NEW)
- Caption generation trigger (already exists, verify ordering)
- ActivityPub + ATProto + IPFS federation (already exists, verify ordering)
- PeerTube-compatible `files[]` and `streamingPlaylists[]` in video API response
- Visibility restriction: processing videos visible only to owner/moderators/admins
- Structured completion logging
- Database migration for `wait_transcoding` column

### Out of Scope

- Frontend player changes (this is backend pipeline only)
- tus resumable upload protocol (deferred feature)
- External transcoding runners (Vidra uses in-process FFmpeg)
- Video password protection

## Approach

**Chosen:** Extend existing encoding service with PeerTube-compatible state machine

**Why:** The current encoding service already handles 80% of the pipeline (resolution encoding, S3, IPFS, ATProto, ActivityPub, captions, notifications). The gaps are: no `waitTranscoding` support, no incremental resolution availability, no original file cleanup, no WebTorrent generation, and no visibility filtering. Extending the existing service preserves all working code while adding the missing steps in PeerTube's order.

**Alternatives considered:**
- *New pipeline orchestrator service:* Would duplicate the existing encoding service's wiring. Rejected — the current service is already well-structured.
- *Job-per-resolution (PeerTube model):* PeerTube creates separate transcoding jobs per resolution. Vidra already encodes in parallel within one job. Changing this would require rewriting the worker model. Rejected — the current parallel-in-one-job approach works and we can still achieve incremental availability by updating OutputPaths after each goroutine completes.

## Context for Implementer

> Write for an implementer who has never seen the codebase.

### Patterns to follow

- **Upload service:** `internal/usecase/upload/service.go:220` — `CompleteUpload()` assembles chunks, sets status to `StatusQueued`, creates `EncodingJob`. This is where `waitTranscoding` logic goes.
- **Encoding pipeline:** `internal/usecase/encoding/service.go:282` — `processJob()` is the main pipeline function. Currently: encode → IPFS → thumbnails → updateVideoInfo → S3 → federation → notifications → captions. The incremental updates and new steps go here.
- **Per-resolution encoding:** `internal/usecase/encoding/service.go` — `encodeResolutions()` spawns goroutines per resolution. Each goroutine calls `update()` after completion. This is where per-resolution OutputPaths updates go.
- **Video domain:** `internal/domain/video.go:42` — `Video` struct with `OutputPaths map[string]string`, `S3URLs map[string]string`, `ProcessedCIDs map[string]string`.
- **Config loading:** `internal/config/config_load.go:168` — `EnableEncoding` pattern. Follow the same for `KeepOriginalFile`.
- **Torrent generator:** `internal/torrent/generator.go` — `Generator.GenerateFromVideo()` exists but is never called by the encoding service.
- **Response envelope:** All API responses use `shared.WriteJSON()` / `shared.WriteJSONWithMeta()` from `internal/httpapi/shared/response.go`.

### Conventions

- Error wrapping: `fmt.Errorf("operation: %w", err)`
- Context-first APIs: `ctx context.Context` as first parameter
- Table-driven tests with testify
- Config via env vars with `GetBoolEnv`, `GetEnvOrDefault`

### Key files

| File | Purpose |
|------|---------|
| `internal/usecase/upload/service.go` | Upload completion, encoding job creation |
| `internal/usecase/encoding/service.go` | Main encoding pipeline (processJob) |
| `internal/domain/video.go` | Video model, statuses, encoding types |
| `internal/config/config.go` | Config struct definition |
| `internal/config/config_load.go` | Config loading from env |
| `internal/repository/video_queries.go` | Video List/Search (already filters status=completed) |
| `internal/repository/video_mutations.go` | Video update mutations |
| `internal/httpapi/handlers/video/handlers.go` | Video handler dependencies |
| `internal/httpapi/handlers/video/upload_handlers.go` | Upload HTTP handlers |
| `internal/torrent/generator.go` | WebTorrent file generation |
| `internal/port/video_repository.go` or `internal/usecase/` interfaces | Repository interfaces |

### Gotchas

- `video_queries.go:192` — `List()` already filters `WHERE privacy = 'public' AND status = 'completed'`. Processing videos are already hidden from public lists. But the single-video GET handler has no such filter — it returns any video by ID.
- `encodeResolutions()` runs all resolutions in parallel goroutines. To update OutputPaths per-resolution, each goroutine needs access to the video repo and must handle concurrent map writes (use mutex).
- The torrent `Generator` requires `[]VideoFile` with file paths and sizes. This info is available after encoding but must be collected.
- `uploadHLSToS3()` already uploads everything including source video. But it's only called if `s3Backend != nil`. The "move to S3" step needs no new code — just verify the wiring works.
- ActivityPub/ATProto federation is already in `processJob()` at lines 348-361. Needs to be verified it fires only after encoding completes (not during incremental updates).

### Domain context

**PeerTube State Machine (reference):**
```
Upload → TO_TRANSCODE (if transcoding enabled)
       → PUBLISHED (if transcoding disabled)

TO_TRANSCODE → TO_MOVE_TO_EXTERNAL_STORAGE (if object storage enabled)
             → PUBLISHED (if object storage disabled)

TO_MOVE_TO_EXTERNAL_STORAGE → PUBLISHED
```

**Vidra Core State Machine (current):**
```
Upload → StatusQueued → StatusProcessing → StatusCompleted
```

**Vidra Core State Machine (proposed):**
```
Upload → waitTranscoding=false: StatusCompleted (published immediately, original available)
       → waitTranscoding=true:  StatusProcessing (owner/mod/admin only)

Encoding proceeds in background for both cases.

After all encoding:
  → If waitTranscoding=true: StatusCompleted (now published)
  → Original file: keep or remove per config
  → S3 migration (if enabled)
  → WebTorrent generation
  → Federation (ActivityPub + ATProto + IPFS)
  → Caption generation
  → Completion logging
```

## Assumptions

- The existing parallel-within-one-job encoding approach is performant enough — supported by the working `encodeResolutions()` goroutine pool. Tasks 3, 4 depend on this.
- `video_queries.go` `List()` and `Search()` filters (`status = 'completed'`) are the only public-facing list endpoints — supported by grep of all handler files. Task 6 depends on this.
- The torrent `Generator.GenerateFromVideo()` works correctly — supported by existing unit tests in `torrent/generator_test.go`. Task 4 depends on this.
- `uploadHLSToS3()` correctly handles all HLS files — supported by existing S3 upload test coverage. Task 4 depends on this.
- The `VideoRepository.Update()` method can update `OutputPaths` partially without overwriting other fields — needs verification. Task 3 depends on this.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Concurrent OutputPaths writes from parallel goroutines cause data races | High | High | Use sync.Mutex around OutputPaths map updates in encodeResolutions; use atomic DB update (JSON merge, not replace) |
| Original file deleted while still being served to a viewer | Medium | High | Only delete original after S3 migration confirms success; add reference counting or delay deletion by configurable grace period |
| WebTorrent generation fails for some resolutions | Low | Medium | Log and continue — WebTorrent is best-effort like IPFS. Don't block the pipeline. |
| Video published before original is fully written to web-accessible path | Low | High | Ensure `CompleteUpload` moves file to final path BEFORE setting status to StatusCompleted |
| Per-resolution DB updates create contention under high load | Medium | Medium | Batch updates: update OutputPaths in a single JSON merge operation per resolution, not full video row update |

## Goal Verification

### Truths

1. When a video is uploaded with `waitTranscoding=false`, the original file is immediately available for playback via the API before encoding starts
2. When a video is uploaded with `waitTranscoding=true`, the video is NOT visible in public search/list endpoints until encoding completes
3. As each resolution finishes encoding, it appears in the video's API response (OutputPaths/files[] grows incrementally)
4. After all encoding completes and `KEEP_ORIGINAL_FILE=false`, the original uploaded file is removed from local storage
5. After all encoding completes and `KEEP_ORIGINAL_FILE=true`, the original file remains available as a resolution option via the API
6. When `ENABLE_S3=true`, all HLS files and media are migrated to S3 storage after encoding
7. Caption generation is triggered after encoding completes
8. Thumbnail and preview images are generated from the video (existing behavior, verified in correct order)
9. A storyboard sprite-sheet is generated for seek-preview and stored as a `VideoStoryboard` record accessible via the API
10. WebTorrent files are generated for each encoded resolution
11. The video is federated via ActivityPub and (if enabled) ATProto after encoding completes
12. When encoding finishes, the video status is `StatusCompleted` and structured logs confirm all pipeline steps

### Artifacts

- `internal/domain/video.go` — `WaitTranscoding` field, `VideoFile` struct, `StreamingPlaylist` struct
- `internal/config/config.go` + `config_load.go` — `KeepOriginalFile` setting
- `migrations/` — `wait_transcoding` column migration
- `internal/usecase/upload/service.go` — `waitTranscoding` handling in `CompleteUpload`
- `internal/usecase/encoding/service.go` — incremental updates, post-processing orchestration, WebTorrent, cleanup, logging
- `internal/httpapi/handlers/video/` — `files[]`/`streamingPlaylists[]` response, visibility filtering

## Progress Tracking

- [x] Task 1: Domain model, config, and migration
- [ ] Task 2: Upload flow — waitTranscoding and immediate original availability
- [ ] Task 3: Per-resolution incremental availability in encoding pipeline
- [ ] Task 4: Post-encoding orchestration (cleanup, WebTorrent, federation, completion logging)
- [ ] Task 5: PeerTube-compatible files[] API response
- [ ] Task 6: Video visibility filtering for processing videos

**Total Tasks:** 6 | **Completed:** 1 | **Remaining:** 5

## Implementation Tasks

### Task 1: Domain Model, Config, and Migration

**Objective:** Add `WaitTranscoding` field to Video, `KeepOriginalFile` config, `VideoFile`/`StreamingPlaylist` types for PeerTube-compatible API, and database migration.

**Dependencies:** None

**Files:**

- Modify: `internal/domain/video.go` — Add `WaitTranscoding bool` to `Video` struct, add `VideoFile` and `StreamingPlaylist` structs
- Modify: `internal/config/config.go` — Add `KeepOriginalFile bool` field to `Config` struct
- Modify: `internal/config/config_load.go` — Load `KEEP_ORIGINAL_FILE` env var (default `true`)
- Create: `migrations/XXXXXX_add_wait_transcoding.sql` — Add `wait_transcoding` boolean column with default `false`
- Test: `internal/domain/video_test.go`, `internal/config/config_test.go`

**Key Decisions / Notes:**

- `WaitTranscoding` maps to PeerTube's `waitTranscoding` field. Default `false` (publish immediately, PeerTube's default).
- `VideoFile` struct: `{ Resolution string, FileUrl string, FileDownloadUrl string, Size int64, Fps float64, MagnetUri string, InfoHash string }` — matches PeerTube's `VideoFile` REST API shape.
- `StreamingPlaylist` struct: `{ Type int, PlaylistUrl string, SegmentsSha256Url string, Files []VideoFile, RedundancyUrls []string }` — matches PeerTube's `StreamingPlaylist` shape.
- `KeepOriginalFile` default `true` matches PeerTube's `transcoding.original_file.keep` default.
- Follow existing config patterns at `config_load.go:168` for `EnableEncoding`.

**Definition of Done:**

- [ ] `WaitTranscoding` field present on `Video` struct with `json:"wait_transcoding" db:"wait_transcoding"` tags
- [ ] `VideoFile` and `StreamingPlaylist` structs defined with PeerTube-compatible JSON tags
- [ ] `KeepOriginalFile` config loaded from `KEEP_ORIGINAL_FILE` env var
- [ ] Migration adds `wait_transcoding BOOLEAN NOT NULL DEFAULT false` column
- [ ] Migration has working `-- +goose Down` section
- [ ] All tests pass

**Verify:**

- `go test ./internal/domain/... -count=1`
- `go test ./internal/config/... -count=1`

---

### Task 2: Upload Flow — waitTranscoding and Immediate Original Availability

**Objective:** Accept `waitTranscoding` parameter during upload, set video status based on it, and make the original file immediately available for playback when `waitTranscoding=false`.

**Dependencies:** Task 1

**Files:**

- Modify: `internal/usecase/upload/service.go` — In `CompleteUpload()`: read `waitTranscoding` from video record, set status based on it, store original file path in `OutputPaths["source"]`
- Modify: `internal/httpapi/handlers/video/upload_handlers.go` — Accept `wait_transcoding` in upload initiation request body, pass to video creation
- Modify: `internal/domain/video.go` — Add `WaitTranscoding` to `VideoCreateRequest` if not already present
- Test: `internal/usecase/upload/service_test.go`, `internal/httpapi/handlers/video/upload_handlers_test.go`

**Key Decisions / Notes:**

- When `waitTranscoding=false`: `CompleteUpload` sets `video.Status = domain.StatusCompleted` (published immediately). The original file at `WebVideoFilePath(videoID, ext)` is stored in `OutputPaths["source"]`. The encoding job is still created and runs in background. This matches PeerTube where `waitTranscoding=false` publishes the video immediately.
- When `waitTranscoding=true`: `CompleteUpload` sets `video.Status = domain.StatusProcessing` (not publicly visible). Same encoding job created.
- The `InitiateUpload` handler already creates the video record. The `waitTranscoding` field should be set there from the request body.
- Reference: `upload/service.go:220` — current `CompleteUpload` sets `StatusQueued`. Change to conditional based on `video.WaitTranscoding`.

**Definition of Done:**

- [ ] Upload initiation accepts `wait_transcoding` boolean parameter
- [ ] `CompleteUpload` sets `StatusCompleted` when `waitTranscoding=false`
- [ ] `CompleteUpload` sets `StatusProcessing` when `waitTranscoding=true`
- [ ] Original file path stored in `OutputPaths["source"]` for both cases
- [ ] Encoding job still created for both cases
- [ ] Tests cover both `waitTranscoding=true` and `waitTranscoding=false` paths
- [ ] All existing upload tests still pass

**Verify:**

- `go test ./internal/usecase/upload/... -count=1`
- `go test ./internal/httpapi/handlers/video/... -run TestUpload -count=1`

---

### Task 3: Per-Resolution Incremental Availability in Encoding Pipeline

**Objective:** As each resolution finishes encoding, immediately update the video's OutputPaths in the database so the API can serve that resolution before all encoding is complete.

**Dependencies:** Task 1

**Files:**

- Modify: `internal/usecase/encoding/service.go` — In `encodeResolutions()`: after each goroutine completes its resolution, update video OutputPaths in DB. Add `onResolutionComplete` callback. In `processJob()`: set video status to `StatusProcessing` at job start (for `waitTranscoding=false` videos that are already `StatusCompleted`, don't change status — they stay published).
- Modify: `internal/repository/video_mutations.go` — Add `AppendOutputPath(ctx, videoID, key, path string)` method that does a JSON merge update (not full replace) to avoid concurrent write issues.
- Modify: `internal/port/` or `internal/usecase/` interfaces — Add `AppendOutputPath` to `VideoRepository` interface.
- Test: `internal/usecase/encoding/service_unit_test.go`, `internal/repository/video_mutations_test.go`

**Key Decisions / Notes:**

- Use `sync.Mutex` in `encodeResolutions` to protect the shared OutputPaths map in-memory, then call `AppendOutputPath` per-resolution.
- DB update uses PostgreSQL `jsonb_set` or `||` operator for atomic JSON merge: `UPDATE videos SET output_paths = output_paths || $1::jsonb WHERE id = $2`. This prevents concurrent goroutines from overwriting each other's updates.
- The progress aggregator at `service.go` already tracks per-resolution progress. Extend this pattern for per-resolution completion.
- Do NOT change video status during incremental updates. Status transitions happen only in `CompleteUpload` (Task 2) and at pipeline end (Task 4).

**Definition of Done:**

- [ ] After each resolution finishes encoding, its HLS playlist path appears in video's `OutputPaths` in the database
- [ ] The API can return the newly available resolution immediately (no need to wait for all resolutions)
- [ ] Concurrent resolution completions don't cause data races or lost updates
- [ ] `AppendOutputPath` uses atomic JSON merge (not full row replace)
- [ ] Tests verify incremental OutputPaths updates
- [ ] All existing encoding tests still pass

**Verify:**

- `go test -race ./internal/usecase/encoding/... -count=1`
- `go test ./internal/repository/... -run TestVideo -count=1`

---

### Task 4: Post-Encoding Orchestration (Cleanup, WebTorrent, Federation, Completion Logging)

**Objective:** After all resolutions are encoded, execute the remaining pipeline steps in PeerTube order: original file handling, S3 migration, WebTorrent generation, federation (ActivityPub + ATProto + IPFS), caption generation, status finalization, and structured completion logging.

**Dependencies:** Task 1, Task 3

**Files:**

- Modify: `internal/usecase/encoding/service.go` — Restructure `processJob()` post-encoding section. Add: `generateStoryboard()`, `cleanupOriginalFile()`, `generateWebTorrents()`, `finalizeVideoState()`, `logPipelineCompletion()`. Wire `torrent.Generator` and storyboard repo as dependencies.
- Modify: `internal/usecase/encoding/service.go` — `NewService()` constructor: accept `torrent.Generator` parameter (or add `WithTorrentGenerator` builder method following existing pattern). Add `WithStoryboardRepo` for storyboard persistence.
- Modify: `internal/app/app.go` — Wire `torrent.Generator` and storyboard repo into encoding service during app startup.
- Modify: `internal/port/video_storyboard.go` — Ensure `Create` method exists on interface for inserting storyboard records.
- Modify: `internal/repository/video_storyboard_repository.go` — Implement `Create` if not present.
- Test: `internal/usecase/encoding/service_unit_test.go`

**Key Decisions / Notes:**

- **Pipeline order after encoding completes** (matches PeerTube):
  1. Generate master HLS playlist (already exists)
  2. Upload variants to IPFS (already exists)
  3. Generate thumbnails/previews (already exists via `generateMediaAssets`)
  4. **[NEW] Generate storyboard** (sprite-sheet preview thumbnails from encoded video using FFmpeg)
  5. Update video info with all paths (already exists)
  6. **[NEW] Generate WebTorrent files** for each encoded resolution
  7. Upload HLS + source + media to S3 (already exists)
  8. **[NEW] Handle original file**: if `KeepOriginalFile=false` AND S3 upload succeeded, remove local original file. If `KeepOriginalFile=true`, keep it and ensure it's in OutputPaths["source"].
  9. Publish to ATProto (already exists)
  10. Publish to ActivityPub (already exists)
  11. Trigger notifications (already exists)
  12. Trigger caption generation (already exists)
  13. **[NEW] Finalize video state**: if `waitTranscoding=true`, set `video.Status = StatusCompleted`
  14. **[NEW] Structured completion log**: log all pipeline step results
- **Storyboard generation**: Use FFmpeg to extract a grid of thumbnails from the encoded video at regular intervals. PeerTube generates a sprite-sheet (e.g., 10 columns, each sprite 160x90px, one sprite per ~2s of video). Save to storage as a single image file. Insert a `VideoStoryboard` record with the sprite dimensions and duration info. The domain model at `internal/domain/video_storyboard.go` and repository at `internal/repository/video_storyboard_repository.go` already exist. FFmpeg command: `ffmpeg -i input.mp4 -vf "fps=1/2,scale=160:90,tile=10x..." -frames:v 1 storyboard.jpg`. Best-effort — log failures, don't block pipeline.
- **WebTorrent generation**: Use existing `torrent.Generator.GenerateFromVideo()`. Collect `VideoFile` structs from encoded resolution files. Store torrent info hash and magnet URI on the video record. Best-effort — log failures, don't block pipeline.
- **Original file cleanup**: Only remove if `config.KeepOriginalFile=false` AND (S3 upload succeeded OR S3 is disabled). Never remove if S3 was supposed to happen but failed.
- **Completion log format**: `slog.Info("video processing complete", "video_id", id, "resolutions", [...], "duration_seconds", N, "s3_migrated", bool, "webtorrents_created", int, "federated_activitypub", bool, "federated_atproto", bool, "ipfs_pinned", bool, "captions_triggered", bool)`

**Definition of Done:**

- [ ] Storyboard sprite-sheet generated from encoded video and stored in DB via `VideoStoryboard` record
- [ ] Storyboard available via existing `GET /api/v1/videos/{id}/storyboards` endpoint after generation
- [ ] WebTorrent files generated for each encoded resolution after encoding completes
- [ ] When `KEEP_ORIGINAL_FILE=false`, original file is removed from local storage after successful S3 upload
- [ ] When `KEEP_ORIGINAL_FILE=true`, original file remains in `OutputPaths["source"]`
- [ ] When `waitTranscoding=true`, video status transitions to `StatusCompleted` after all encoding + post-processing
- [ ] Structured completion log emitted with all pipeline step results
- [ ] Pipeline steps execute in the specified order
- [ ] Existing federation, caption, notification logic unchanged (just reordered if needed)
- [ ] All tests pass including race detector

**Verify:**

- `go test -race ./internal/usecase/encoding/... -count=1`
- `go test ./internal/app/... -count=1`

---

### Task 5: PeerTube-Compatible files[] API Response

**Objective:** Add PeerTube-compatible `files[]` and `streamingPlaylists[]` arrays to the video GET API response, built from the video's OutputPaths, S3URLs, and torrent info.

**Dependencies:** Task 1, Task 3

**Files:**

- Create: `internal/httpapi/handlers/video/video_files_response.go` — Helper function `buildVideoFilesResponse(video *domain.Video) ([]domain.VideoFile, []domain.StreamingPlaylist)` that constructs the PeerTube-compatible files arrays from OutputPaths/S3URLs.
- Modify: `internal/httpapi/handlers/video/` — Whichever handler returns the single video GET response — add `files` and `streamingPlaylists` fields.
- Test: `internal/httpapi/handlers/video/video_files_response_test.go`

**Key Decisions / Notes:**

- PeerTube video GET response includes:
  ```json
  {
    "files": [{"resolution": {"id": 720, "label": "720p"}, "magnetUri": "...", "size": 12345, "fileUrl": "...", "fileDownloadUrl": "..."}],
    "streamingPlaylists": [{"type": 1, "playlistUrl": "...", "files": [...]}]
  }
  ```
- `files[]` = web video files (original + any non-HLS encoded files). In Vidra, this is the original source file when kept.
- `streamingPlaylists[]` = HLS playlists. `type: 1` = HLS. Each playlist has its own `files[]` with per-resolution entries.
- Build from `video.OutputPaths` (local paths) and `video.S3URLs` (S3 URLs). Prefer S3URLs when available, fall back to local paths.
- Include resolution ID (height int) and label (e.g., "720p") for each file.
- Include `magnetUri` and `infoHash` from torrent data when available (may be empty if WebTorrent not generated yet).

**Definition of Done:**

- [ ] Video GET API response includes `files[]` array with web video files
- [ ] Video GET API response includes `streamingPlaylists[]` array with HLS playlists and per-resolution files
- [ ] Each file entry includes `resolution`, `fileUrl`, `fileDownloadUrl`, `size` (where available)
- [ ] S3 URLs preferred over local paths when both exist
- [ ] Response matches PeerTube REST API shape for video files
- [ ] Empty arrays returned (not null) when no files exist yet
- [ ] Tests verify response shape for various states (no files, partial, complete)

**Verify:**

- `go test ./internal/httpapi/handlers/video/... -run TestVideoFiles -count=1`

---

### Task 6: Video Visibility Filtering for Processing Videos

**Objective:** Ensure that videos with `waitTranscoding=true` that are still processing are only visible to the uploader, moderators, and admins — not in public search, explore, or to other users.

**Dependencies:** Task 2

**Files:**

- Modify: `internal/httpapi/handlers/video/` — The single video GET handler: add check that if `video.Status != StatusCompleted`, only the owner (matching `userID` from JWT), moderators, or admins can view it. Return 404 for other users.
- Modify: `internal/httpapi/handlers/video/me_handlers.go` — `GetMyVideosHandler`: ensure it returns videos in ALL statuses for the authenticated user (including processing).
- Verify: `internal/repository/video_queries.go:192` — `List()` already filters `status = 'completed'`. Confirm `Search()` at line 287 does the same.
- Test: `internal/httpapi/handlers/video/visibility_test.go`

**Key Decisions / Notes:**

- `video_queries.go:192` — `List()` already has `WHERE privacy = 'public' AND status = 'completed'`. Processing videos are already excluded from public lists. ✅
- `video_queries.go:287` — `Search()` has the same filter. ✅
- The gap is the **single video GET by ID** — currently returns any video regardless of status. Need to add: if `status != StatusCompleted` AND requesting user is not the owner/moderator/admin → return 404.
- Moderator/admin check: use `middleware.AuthUserRole` from context to check role. Roles: `admin`, `moderator`, `user`.
- `GetMyVideosHandler` at `me_handlers.go:12` should already return all user's videos. Verify it doesn't filter by status.
- Processing videos should show their current state: status, encoding progress, resolutions completed so far (OutputPaths).

**Definition of Done:**

- [ ] Video GET by ID returns 404 for non-completed videos when requester is not owner/moderator/admin
- [ ] Video GET by ID returns full video (including processing status and partial resolutions) for owner/moderator/admin
- [ ] Public list/search endpoints exclude non-completed videos (verify existing behavior)
- [ ] `GET /api/v1/users/me/videos` returns videos in all statuses for the authenticated user
- [ ] Tests cover: owner sees processing video, admin sees processing video, anonymous user gets 404 for processing video, public user gets 404 for processing video
- [ ] All existing video handler tests still pass

**Verify:**

- `go test ./internal/httpapi/handlers/video/... -count=1`

---

## Open Questions

None — all design decisions resolved via user Q&A.

### Deferred Ideas

- **Per-video `keepOriginalFile` override** — User chose global config only for now. Could add per-video override later.
- **Webhook notifications for encoding progress** — Could notify external systems as each resolution completes.
- **Priority encoding queue** — Higher-priority users get their videos encoded first.
