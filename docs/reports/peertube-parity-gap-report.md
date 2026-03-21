# PeerTube Compatibility Validation Report

Generated: 2026-03-21
PeerTube references:
- https://github.com/chocobozzz/peertube
- https://docs.joinpeertube.org/api-rest-reference.html

## Scope

This report replaces the earlier optimistic parity snapshot with a validation-driven status check based on:

- Athena route and handler audit in `internal/httpapi/routes.go`
- Athena OpenAPI validation and code generation
- Focused Go verification: `env -u GOROOT go test -short ./internal/generated ./internal/httpapi ./internal/httpapi/handlers/... ./tests/integration ./tests/e2e/scenarios`
- Containerized Newman execution against the Docker `test` profile

## Bottom Line

Athena has broad PeerTube-shaped API coverage and meaningful Athena-only extensions, but it is **not yet a drop-in PeerTube instance target**.

What is true today:

- `api/openapi.yaml` now validates cleanly and regenerates `internal/generated/types.go`
- Focused Go tests pass across generated types, HTTP handlers, integration tests, and E2E scenarios
- The Docker E2E stack now boots reliably on a fresh database
- Stateful Newman execution is now real, not synthetic

What is not true yet:

- Runtime federation discovery is not working like PeerTube
- Several not-found paths return `500` where PeerTube clients expect `404`
- A number of Postman collections still assert Athena-native or stale response shapes instead of PeerTube-compatible ones
- Some PeerTube-canonical endpoints remain absent or only exist behind Athena-specific paths

Current Newman result:

- Collections run: 12
- Passed: 2
- Failed: 10
- Passing collections: `athena-feeds`, `athena-secure-messaging`

## Infrastructure Fixes Applied During Validation

The validation uncovered and fixed several test-environment blockers before any parity conclusions were trustworthy:

- `postman/run-all-tests.sh` now runs a stateful sequence and exports the working environment between collections
- `docker-compose.yml` `newman` service now runs the full Postman sequence, not just auth
- test database bootstrap no longer mounts `init-test-db.sql`, which conflicted with embedded Goose migrations
- `migrations/025_create_oauth_clients_table.sql` and `migrations/028_create_oauth_authorization_codes_table.sql` no longer nest `BEGIN/COMMIT` inside Goose transactions
- `postman/run-all-tests.sh` uses an Alpine-safe `mktemp` pattern
- validation-mode rate limits are relaxed so later collections are not polluted by `429` noise
- OpenAPI duplicate paths and duplicate `searchVideos` operation IDs were fixed

These changes improved signal quality; the remaining failures are now mostly real contract or behavior gaps.

## What Athena Already Covers Well

Athena does have a substantial PeerTube-shaped surface area:

- Public config and about routes: `/api/v1/config`, `/api/v1/config/about`, `/api/v1/instance/stats`
- Search routes: `/api/v1/videos/search` and `/api/v1/search/videos`
- User quota and subscription helpers: `/api/v1/users/me/video-quota-used`, `/api/v1/users/me/subscriptions/exist`
- Watch history routes: `/api/v1/users/me/history/videos`
- Feeds: public feeds and authenticated subscription feeds
- Server follower/following routes
- OAuth client bootstrap route: `/api/v1/oauth-clients/local`
- User and server blocklist routes
- Activity/federation admin surfaces
- Athena-only extensions: encrypted messaging, crypto payments, IPFS, livestreaming, ATProto-related surfaces

Validated by E2E:

- Feeds behave well enough to pass Newman
- Secure messaging passes a full multi-user E2E flow, including key exchange, encrypted send/reply, and authorization failures

## Highest-Risk PeerTube Compatibility Gaps

These are the gaps most likely to break a PeerTube-style instance import or client behavior.

| Gap | Evidence | Why it matters |
| --- | --- | --- |
| Federation discovery missing at runtime | Newman: `/.well-known/webfinger`, `/.well-known/nodeinfo`, `/nodeinfo/2.0` all return `404`; app logs show `NOT_FOUND` and only register `/.well-known/atproto-did` in the test app | PeerTube and ActivityPub discovery depend on these endpoints |
| Video not-found paths return `500` | Newman failures on `GET /api/v1/videos/{id}`, `PUT /api/v1/videos/{id}`, and stream lookup for nonexistent IDs | PeerTube clients expect deterministic `404`/`400`, not internal errors |
| PeerTube canonical admin paths still differ | Athena still uses Athena-specific/admin-prefixed variants for some operations | Imported tooling expecting exact PeerTube routes will break |
| Handle-based compatibility is incomplete | Several flows still use Athena UUID/ID semantics or Athena-native `/channels` paths | PeerTube imports and external clients expect account/channel handles |

PeerTube-canonical path gaps still visible in the route/spec comparison:

- `/api/v1/users/registrations*` is not exposed at PeerTube’s path
- `/api/v1/jobs/{state}` is not exposed at PeerTube’s path
- `/api/v1/plugins*` remains Athena-admin-shaped rather than PeerTube-canonical
- `/api/v1/video-channels/{channelHandle}/collaborators*` is not present
- `/api/v1/videos/imports/{id}/cancel` and `/api/v1/videos/imports/{id}/retry` aliases are not present
- `/api/v1/runners*` is still absent

## Missing or Incomplete User Stories

The following user stories are not yet fully satisfied end to end:

- Import a PeerTube instance and have remote actors resolve through WebFinger and NodeInfo without Athena-specific adaptation
- Import PeerTube channels that rely on handle-based discovery and collaborator management
- Retry or cancel imported videos using PeerTube’s canonical import lifecycle endpoints
- Administer PeerTube-style registration approvals through PeerTube-compatible registration moderation routes
- Use plugin and runner management with PeerTube-compatible endpoints
- Access video detail, update, and streaming endpoints with predictable PeerTube-style error semantics for nonexistent IDs

Athena-only user stories that exist in code but are not yet fully proven by stateful Newman coverage:

- Crypto payments
- IPFS-backed flows
- ATProto interoperability
- End-to-end livestream lifecycle

## Newman Coverage Status

Current state of the stateful Postman/Newman suite:

| Collection | Result | Primary issue type |
| --- | --- | --- |
| `athena-feeds` | Pass | Good PeerTube-style coverage |
| `athena-secure-messaging` | Pass | Athena-only extension validated |
| `athena-auth` | Fail | Video upload assertions expect top-level `id`; video lookup/stream not-found paths return `500` |
| `athena-videos` | Fail | Same `500` not-found problem plus stale response-shape expectations |
| `athena-uploads` | Fail | Missing fixture file, stale field expectations, mixed response-shape drift |
| `athena-channels` | Fail | Creation/setup assumptions and Athena-native path/ID expectations |
| `athena-instance-config` | Fail | Contract mismatch: expected `serverVersion`; admin request is not authenticated as admin |
| `athena-imports` | Fail | Contract mismatch between collection and actual wrapped responses; several import edge cases still return `500` |
| `athena-blocklist` | Fail | Account blocklist flow and pagination/total expectations drift from actual response shape |
| `athena-notifications` | Fail | Single-item mark-read/delete seed-data issues and response-shape assumptions |
| `athena-livestreaming` | Fail | Missing seeded stream IDs; create/get/end flow not fully stateful |
| `athena-federation` | Fail | Real runtime discovery gap (`404`) |

Important distinction:

- Some failures are real product issues: federation discovery, `500` on missing video/import lookups
- Some failures are collection drift: top-level vs wrapped response assertions, missing seed IDs, outdated field names
- Both matter for PeerTube compatibility, because a compatibility claim requires the contract and the implementation to agree

## OpenAPI Status

OpenAPI is in better shape than runtime parity:

- `api/openapi.yaml` validates
- generated types build again
- duplicate path keys were removed
- duplicate search operation IDs were separated

However, documentation is still ahead of runtime in at least one important area:

- Federation discovery endpoints are represented in docs/code, but are not mounted in the containerized runtime used for validation

That mismatch must be resolved before Athena can be presented as PeerTube-compatible for import and federation workflows.

## Recommended Next Steps

1. Fix ActivityPub discovery route registration so `/.well-known/webfinger`, `/.well-known/nodeinfo`, and `/nodeinfo/2.0` are live whenever ActivityPub is enabled.
2. Fix video and import handlers so missing resources return `404` instead of `500`.
3. Add the remaining PeerTube-canonical aliases and paths:
   `users/registrations`, `jobs/{state}`, `plugins`, import `cancel`/`retry`, channel collaborators.
4. Split Postman collections into:
   PeerTube-compat collections and Athena-extension collections.
5. Update failing Postman assertions to use Athena’s wrapped response format only where that format is intentional and documented.
6. Add a true “import PeerTube instance” E2E scenario that verifies:
   discovery, remote actor resolution, channel mapping, imported videos, and normal follow/watch behavior after import.

## Confidence Statement

Confidence is high that Athena has a strong foundation and substantial PeerTube overlap.

Confidence is also high that **full PeerTube instance import compatibility is not complete yet**, because the latest validation still shows:

- broken federation discovery at runtime
- incorrect `500` behavior on missing video/import paths
- unresolved canonical-route gaps
- E2E contract drift across 10 of 12 stateful collections
