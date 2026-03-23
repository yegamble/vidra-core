# PeerTube Compatibility Status

**Last Updated**: 2026-03-23
**Scope**: PeerTube-aligned API/runtime compatibility for Vidra Core native APIs, plus Vidra Core-only features that must coexist with that compatibility

## Summary

Vidra Core currently has a strong, validated PeerTube-shaped runtime surface. OpenAPI verification passes, `go test -short ./...` passes, and the Docker-backed Newman suite passes across 19 stateful collections covering PeerTube-style routes and Vidra Core-specific extensions.

That said, Vidra Core is **not yet feature-complete for full PeerTube instance migration**. The main missing capability is a real ETL/import pipeline that can ingest a PeerTube database dump and storage layout into Vidra Core automatically.

## Compatibility Matrix

| Area | Vidra Core Endpoints | PeerTube Capability | Status | Notes |
|---|---|---|---|---|
| Channels | `/api/v1/channels`, `/api/v1/channels/{id}`, `/api/v1/channels/{id}/videos` | Video channels as first-class publishers | ✅ Implemented | Validated in Go and Newman |
| Channel subscriptions | `/api/v1/channels/{id}/subscribe`, `/api/v1/channels/{id}/subscribers`, `/api/v1/videos/subscriptions` | Channel follow + subscription feed | ✅ Implemented | Legacy compatibility shims still exist |
| Comments (threaded) | `/api/v1/videos/{videoId}/comments`, `/api/v1/comments/{commentId}` | Threaded comments + moderation | ✅ Implemented | Comment threading and moderation validated |
| Ratings | `/api/v1/videos/{videoId}/rating` | Like/dislike interactions | ✅ Implemented | Covered by existing tests |
| Playlists | `/api/v1/playlists`, `/api/v1/playlists/{playlistId}`, `/api/v1/playlists/{playlistId}/items` | Playlist CRUD + item management | ✅ Implemented | Core behavior implemented |
| Captions | `/api/v1/videos/{id}/captions`, `/api/v1/videos/{id}/captions/{captionId}` | Subtitle upload/list/delete/fetch | ✅ Implemented | Core caption flows implemented |
| OAuth2 and instance metadata | `/oauth/*`, `/api/v1/config`, `/api/v1/config/about`, `/oembed` | Auth bootstrap, about/config, oEmbed | ✅ Implemented | Covered in runtime validation |
| Imports | `/api/v1/videos/imports`, PeerTube-style aliases on import inputs | Import lifecycle APIs | ✅ Implemented | Runtime semantics validated; ETL importer still missing |
| Registrations, jobs, plugins, runners | `/api/v1/admin/*`, PeerTube-canonical aliases | Admin and worker lifecycle surfaces | ✅ Implemented | Live-validated in Newman |
| Federation discovery | `/.well-known/webfinger`, `/.well-known/nodeinfo`, `/nodeinfo/2.0` | ActivityPub/NodeInfo discovery | ✅ Implemented | E2E validated |
| Full PeerTube instance migration | N/A | Import PeerTube DB + media and keep behavior intact | ⚠️ Partial | Planning/documentation exists, but no shipped importer/ETL tool |

## OpenAPI Coverage

PeerTube-aligned and adjacent routes are documented in:

- `api/openapi.yaml`
- `api/openapi_channels.yaml`
- `api/openapi_comments.yaml`
- `api/openapi_ratings_playlists.yaml`
- `api/openapi_captions.yaml`
- `api/openapi_moderation.yaml`
- `api/openapi_federation.yaml`
- `api/openapi_social.yaml`
- `api/openapi_payments.yaml`
- `api/openapi_ipfs.yaml`

A compatibility tag remains present for matching operations:

- `PeerTube-Compat`

## Extra Vidra Core Features

Vidra Core extends beyond PeerTube and these extra surfaces are also validated in the Docker test profile:

- Secure messaging
- IOTA payments
- IPFS routes
- ATProto social flows
- Livestreaming

## What Is Verified

The current validation baseline proves:

- `./scripts/verify-openapi.sh` passes
- `env -u GOROOT go test -short ./...` passes
- 19 selected Newman collections pass against the live Docker `test` profile
- PeerTube-canonical route aliases are exercised in the same stateful suite as Vidra Core-native routes
- ATProto, payments, secure messaging, and IPFS do not break the PeerTube-shaped runtime surface

## Intentional Deviations

These are current differences from strict one-to-one PeerTube parity:

1. Pagination style is still mixed by endpoint family.
2. Response envelope conventions differ by handler family.
3. Vidra Core is multi-protocol and multi-feature by design, including ATProto, messaging, payments, and IPFS in the same product surface.

## Remaining Gaps

1. Vidra Core does not yet ship a real PeerTube ETL/importer for database plus media migration.
2. Vidra Core does not yet run fixture-based migration rehearsals proving imported PeerTube data behaves correctly after cutover.
3. Vidra Core does not yet run an upstream PeerTube UI/client compatibility suite.
4. Full one-to-one schema parity is still not guaranteed for every list/detail payload.

## Next Actions

1. Implement a real PeerTube dump/media import pipeline with dry-run and validation reporting.
2. Add fixture-based migration E2E coverage for users, channels, videos, comments, playlists, captions, and subscriptions.
3. Add an upstream compatibility harness to detect API/behavior drift against PeerTube itself.
