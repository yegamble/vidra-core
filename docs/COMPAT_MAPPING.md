# Compat Route Mapping Guide

This guide outlines how to expose PeerTube‑shaped endpoints (the "Compat" layer) by wrapping Athena’s existing handlers and translating payloads.

Use this to quickly implement the placeholders listed under `x-compat-placeholders` in `api/openapi.yaml`.

## Goals

- Keep existing Athena routes/services untouched.
- Add a thin facade that adapts request/response shapes to PeerTube conventions.
- Keep both native and compat routes during transition.

## Routes to Map (Phase 1)

- `GET /compat/api/v1/videos` → map to `GET /api/v1/videos`
- `GET /compat/api/v1/videos/{id}` → map to `GET /api/v1/videos/{id}`
- `GET /compat/api/v1/videos/{id}/streaming-playlists` → map to `GET /api/v1/videos/{id}/stream`
- `GET /compat/api/v1/users/{id}` → map to `GET /api/v1/users/{id}`
- `GET /compat/api/v1/users/{id}/videos` → map to `GET /api/v1/users/{id}/videos`
- `POST /compat/api/v1/videos/upload` → map to `POST /api/v1/videos/upload`

## Wiring Pattern (Chi)

Add a small group in `internal/httpapi/routes.go` (or a new `compat_routes.go`) that forwards to wrapper handlers:

```go
r.Route("/compat/api/v1", func(r chi.Router) {
    r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/videos", compat.ListVideos(videoRepo))
    r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/videos/{id}", compat.GetVideo(videoRepo))
    r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/videos/{id}/streaming-playlists", compat.GetStreamingPlaylists(videoRepo))
    r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/users/{id}", compat.GetUser(userRepo))
    r.With(middleware.OptionalAuth(cfg.JWTSecret)).Get("/users/{id}/videos", compat.GetUserVideos(videoRepo))
    r.With(middleware.Auth(cfg.JWTSecret)).Post("/videos/upload", compat.UploadVideoFile(videoRepo, cfg))
})
```

## Wrapper Structure

Each wrapper should:

1) Parse inputs to Athena’s shape (query, params).
2) Call the underlying repo/service.
3) Transform the response to PeerTube’s expected fields.

Example (sketch):

```go
// Package compat contains thin mappers from Athena -> PeerTube shapes.
package compat

func ListVideos(videoRepo usecase.VideoRepository) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // 1) Translate query params (page, sort, search) to Athena filters
        filters := parseCompatQuery(r)

        // 2) Fetch from Athena
        vids, total, err := videoRepo.List(r.Context(), filters)
        if err != nil { WriteError(w, http.StatusInternalServerError, err); return }

        // 3) Map to PeerTube fields
        out := mapVideosToPeerTube(vids, total, filters)
        WriteJSON(w, http.StatusOK, out)
    }
}
```

## Mapping Notes

- IDs/Slugs: Prefer stable UUIDs; expose slugs where PeerTube expects them.
- Thumbnails/Previews: Athena already stores IPFS or local paths; map to absolute URLs in compat responses.
- Streaming: For `/streaming-playlists`, return a list/structure of HLS URLs and qualities that match PeerTube clients’ expectations.
- Users vs Channels: Until channels are added, map a user to a single default channel in compat responses and document this behavior.
- Pagination: PeerTube typically uses pagination meta (`total`, `page`, `pageSize`); compute from Athena’s list responses.

## Testing Tips

- Add handler‑level tests that call compat endpoints and snapshot a small, known response payload.
- Reuse existing `internal/testutil` helpers for time/IDs.

## Implementation Sequence

1) Add the chi group shown above and create a `internal/httpapi/compat` package with wrappers.
2) Implement `ListVideos` and `GetVideo` first; verify PeerTube client UIs can browse.
3) Add `streaming-playlists` mapping; ensure HLS plays as expected.
4) Implement user mappings and upload compat endpoint.
5) Iterate on field names and pagination once you have the spec locally.

