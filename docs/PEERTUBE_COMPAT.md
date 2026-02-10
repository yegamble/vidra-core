# PeerTube Compatibility Status

**Last Updated**: 2026-02-10
**Scope**: PeerTube-inspired API/resource compatibility for Athena native APIs (no facade layer)

## Summary

Athena now implements the major PeerTube-aligned API areas (channels, channel subscriptions, comments, ratings, playlists, captions, OAuth2, instance/oEmbed, and ActivityPub discovery). Compatibility is functional for common client flows, with a small set of intentional deviations documented below.

## Compatibility Matrix

| Area | Athena Endpoints | PeerTube Capability | Status | Notes |
|---|---|---|---|---|
| Channels | `/api/v1/channels`, `/api/v1/channels/{id}`, `/api/v1/channels/{id}/videos` | Video channels as first-class publishers | ✅ Implemented | Channel/account model is active; videos are channel-linked |
| Channel subscriptions | `/api/v1/channels/{id}/subscribe`, `/api/v1/channels/{id}/subscribers`, `/api/v1/videos/subscriptions` | Channel follow + subscription feed | ✅ Implemented | Legacy user subscription endpoints remain as compatibility shims |
| Comments (threaded) | `/api/v1/videos/{videoId}/comments`, `/api/v1/comments/{commentId}`, `/api/v1/comments/{commentId}/flag`, `/api/v1/comments/{commentId}/moderate` | Threaded comments + moderation | ✅ Implemented | Reply threading via `parentId`; moderation + flagging supported |
| Ratings | `/api/v1/videos/{videoId}/rating` | Like/dislike interactions | ✅ Implemented | Idempotent rating behavior covered by tests |
| Playlists | `/api/v1/playlists`, `/api/v1/playlists/{playlistId}`, `/api/v1/playlists/{playlistId}/items` | Playlist CRUD + item management | ✅ Implemented | Includes ordering endpoint and watch-later convenience |
| Captions | `/api/v1/videos/{id}/captions`, `/api/v1/videos/{id}/captions/{captionId}`, `/api/v1/videos/{id}/captions/{captionId}/content` | Subtitle upload/list/delete/fetch | ✅ Implemented | VTT/SRT with language metadata supported |
| OAuth2 | `/oauth/token`, `/oauth/authorize`, `/oauth/revoke`, `/oauth/introspect` | OAuth2 auth flows | ✅ Implemented | Includes auth code + revocation + introspection |
| Instance metadata | `/api/v1/instance/about`, `/oembed` | Instance/about + oEmbed | ✅ Implemented | Admin instance config APIs also available |
| Federation discovery | `/.well-known/webfinger`, `/.well-known/nodeinfo`, `/nodeinfo/2.0` | ActivityPub/NodeInfo discovery | ✅ Implemented | Actor and inbox/outbox endpoints implemented |

## OpenAPI Coverage

PeerTube-aligned routes are documented in:

- `api/openapi_channels.yaml`
- `api/openapi_comments.yaml`
- `api/openapi_ratings_playlists.yaml`
- `api/openapi_captions.yaml`
- `api/openapi_moderation.yaml`
- `api/openapi_federation.yaml`
- `api/openapi.yaml` (OAuth2 + core)

A compatibility tag is now present across these specs for matching operations:

- `PeerTube-Compat`

## Intentional Deviations

These are current intentional differences from strict PeerTube shape parity:

1. Pagination style is mixed by endpoint family.
- Some endpoints use `page/pageSize`; others use `limit/offset`.

2. Response envelope conventions differ by handler family.
- Many handlers return wrapped payloads (for example `data`, `pagination`, `success`) rather than a single global envelope standard.

3. Legacy compatibility routes are retained.
- User-based subscription routes remain available while channel-based subscriptions are canonical.

4. Federation is multi-protocol.
- Athena includes ATProto in addition to ActivityPub; this extends beyond strict PeerTube scope.

## Test Verification

PeerTube-relevant behavior is already covered in existing handler integration/unit suites:

- Channels/subscriptions:
  - `internal/httpapi/handlers/channel/channel_subscriptions_integration_test.go`
  - `internal/httpapi/handlers/channel/subscriptions_backward_compat_test.go`
  - `internal/httpapi/handlers/channel/subscriptions_pagination_test.go`
- Comments/threading:
  - `internal/httpapi/handlers/social/comments_integration_test.go`
- Ratings/playlists:
  - `internal/httpapi/handlers/social/ratings_playlists_integration_test.go`
- Captions:
  - `internal/httpapi/handlers/social/captions_integration_test.go`
- Instance/oEmbed:
  - `internal/httpapi/handlers/moderation/moderation_test.go`
  - `internal/httpapi/handlers/moderation/moderation_integration_test.go`
- ActivityPub NodeInfo discovery:
  - `internal/httpapi/handlers/federation/activitypub_test.go`

## Remaining Gaps

1. Full one-to-one response-schema parity with PeerTube for every list/details payload.
2. Uniform pagination contract across all compatibility routes.
3. Expanded compatibility conformance tests that assert strict JSON schema parity against PeerTube fixtures.

## Next Actions

1. Add schema-level compatibility assertions for channels/comments/playlists/captions response contracts.
2. Standardize pagination contract for compat-tagged routes.
3. Keep legacy shims time-boxed and remove once clients have migrated.
