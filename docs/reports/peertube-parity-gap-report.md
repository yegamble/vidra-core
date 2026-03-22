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

Athena now has a **green validation baseline** for the current documented/OpenAPI + Postman surface, but it is **still not yet a full drop-in PeerTube instance target**.

What is true today:

- `api/openapi.yaml` validates cleanly and regenerates `internal/generated/types.go`
- Focused Go tests pass across generated types, HTTP handlers, integration tests, and E2E scenarios
- The Docker E2E stack boots reliably on a fresh database
- Stateful Newman execution is real and now passes end to end, including PeerTube-canonical alias coverage
- Runtime federation discovery is mounted in the validated container runtime
- Missing-video and missing-import error paths now return PeerTube-style `404`/`400` semantics instead of `500`
- Channel subscription and livestream flows now pass their real stateful E2E paths
- PeerTube-canonical registration, jobs, plugins, runners, import lifecycle, and handle-based collaborator paths are now mounted at the expected URLs
- PeerTube-canonical registration routes remain reserved even when registration moderation backing is absent, so they no longer fall through to `/api/v1/users/{id}`

What is not true yet:

- Several PeerTube-canonical endpoint families now exist at the right paths, but collaborator, runner, registration-moderation, and plugin-write flows are still thin compatibility shims or `501 Not Implemented` stubs
- There is still no true “import an existing PeerTube instance and use it normally” E2E scenario
- Athena-only extensions such as crypto payments, IPFS-native flows, and ATProto interoperability are not yet proven by the stateful Newman suite

Current Newman result:

- Collections run: 13
- Passed: 13
- Failed: 0
- Passing collections: `athena-auth`, `athena-videos`, `athena-uploads`, `athena-channels`, `athena-instance-config`, `athena-imports`, `athena-peertube-canonical`, `athena-feeds`, `athena-blocklist`, `athena-notifications`, `athena-livestreaming`, `athena-federation`, `athena-secure-messaging`

## Infrastructure Fixes Applied During Validation

The validation uncovered and fixed several test-environment blockers before any parity conclusions were trustworthy:

- `postman/run-all-tests.sh` now runs a stateful sequence and exports the working environment between collections
- `docker-compose.yml` `newman` service now runs the full Postman sequence, not just auth
- test database bootstrap no longer mounts `init-test-db.sql`, which conflicted with embedded Goose migrations
- `migrations/025_create_oauth_clients_table.sql` and `migrations/028_create_oauth_authorization_codes_table.sql` no longer nest `BEGIN/COMMIT` inside Goose transactions
- `postman/run-all-tests.sh` uses an Alpine-safe `mktemp` pattern
- validation-mode rate limits are relaxed so later collections are not polluted by `429` noise
- OpenAPI duplicate paths and duplicate `searchVideos` operation IDs were fixed

These changes improved signal quality enough to produce a green baseline. The remaining compatibility work is now primarily missing-scope work, not a broken validation harness.

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
- PeerTube-canonical route aliases are now exercised in the same stateful suite instead of being left as unverified router wiring

## Highest-Risk PeerTube Compatibility Gaps

These are the gaps most likely to block a true PeerTube-instance import or an external PeerTube client/tooling integration even though the current Athena validation suite is green.

| Gap | Evidence | Why it matters |
| --- | --- | --- |
| PeerTube-canonical admin and maintenance paths are only partially implemented | Route/spec audit and Newman now show `registrations`, `jobs`, canonical import lifecycle aliases, top-level `plugins`, and `runners` paths, but many of those flows still end in explicit compatibility handlers | Imported tooling now finds the expected routes, but non-trivial management flows will still fail until the backing behavior exists |
| Handle/collaborator import stories are incomplete | `/api/v1/video-channels/{channelHandle}/collaborators*` is now mounted and covered in Newman, but currently returns `501 Not Implemented` | Multi-user channel administration is part of real PeerTube instance behavior |
| True import/federation continuation is not E2E-proven | The current suite validates discovery endpoints and import CRUD, but not a full imported remote instance behaving normally afterward | “Drop-in PeerTube instance target” requires more than route availability |
| Athena-only extensions are only partially validated | Secure messaging and livestreaming now pass; crypto payments, IPFS-native flows, and ATProto interoperability are not in the stateful suite | Extra Athena features still need proof beyond route presence |

PeerTube-canonical path coverage now present in the route/spec/runtime comparison:

- `/api/v1/users/registrations*`
- `/api/v1/jobs` and `/api/v1/jobs/{state}`
- `/api/v1/videos/imports/{id}/cancel` and `/api/v1/videos/imports/{id}/retry`
- `/api/v1/plugins*`
- `/api/v1/runners*`
- `/api/v1/video-channels/{channelHandle}/collaborators*`

The remaining problem is no longer missing paths. It is the limited behavior behind several of those paths.

## Missing or Incomplete User Stories

The following user stories are not yet fully satisfied end to end:

- Import a PeerTube instance and continue normal follow/watch/discovery behavior without Athena-specific adaptation
- Import PeerTube channels that rely on handle-based discovery and collaborator management
- Retry or cancel imported videos using PeerTube’s canonical import lifecycle endpoints
- Administer PeerTube-style registration approvals through PeerTube-compatible registration moderation routes
- Use plugin and runner management with PeerTube-compatible endpoints beyond route discovery and basic read-only aliasing

Athena-only user stories that exist in code but are not yet fully proven by stateful Newman coverage:

- Crypto payments
- IPFS-backed flows
- ATProto interoperability

## Newman Coverage Status

Current state of the stateful Postman/Newman suite:

| Collection | Result | What it now proves |
| --- | --- | --- |
| `athena-auth` | Pass | Stateful auth, avatar, and core auth error handling work against the current contract |
| `athena-videos` | Pass | Video CRUD, search, upload, stream edge cases, and not-found behavior align with the validated contract |
| `athena-uploads` | Pass | Chunked uploads and encoding-status endpoints work statefully in Docker |
| `athena-channels` | Pass | Channel CRUD, list/get, subscribe/unsubscribe, and cleanup flows work end to end |
| `athena-instance-config` | Pass | Public config and quota endpoints match the validated contract |
| `athena-imports` | Pass | Import CRUD/error semantics work for the validated Athena surface |
| `athena-peertube-canonical` | Pass | PeerTube-canonical aliases for registrations, jobs, plugins, runners, import cancel/retry, and handle/collaborator paths are mounted and behave according to the current Athena contract, including explicit auth-gated and `501` compatibility-shim behavior |
| `athena-feeds` | Pass | Public and subscription feeds behave correctly |
| `athena-blocklist` | Pass | Account/server blocklist state transitions and pagination work |
| `athena-notifications` | Pass | Notification list/read/delete flows work against wrapped responses |
| `athena-livestreaming` | Pass | Stream create/get/stats/session/channel flows are stateful and working |
| `athena-federation` | Pass | WebFinger, NodeInfo, and related federation discovery endpoints are live in runtime |
| `athena-secure-messaging` | Pass | Athena-only encrypted messaging works across a full multi-user E2E flow |

Important distinction:

- A green suite means the currently claimed contract is now internally consistent and working
- It does **not** yet mean Athena has complete PeerTube endpoint parity or import-story parity

## OpenAPI Status

OpenAPI is in better shape than runtime parity:

- `api/openapi.yaml` validates
- generated types build again
- duplicate path keys were removed
- duplicate search operation IDs were separated

Documentation/runtime alignment is materially better now:

- Federation discovery endpoints are represented in docs/code and are mounted in the validated runtime
- Wrapped response expectations in Postman now match the current documented Athena contract

The remaining OpenAPI/parity risk is mostly scope risk: compatibility handlers that still need full behavior, and missing import-scenario coverage.

## Recommended Next Steps

1. Replace the remaining `501`/compatibility-shim PeerTube routes with real backed behavior:
   collaborator management, runners, plugin write/settings flows, and registration moderation where appropriate.
2. Add a true “import PeerTube instance” E2E scenario that verifies:
   discovery, remote actor resolution, channel mapping, imported videos, and follow/watch behavior after import.
3. Extend stateful coverage for Athena-only extensions:
   crypto payments, IPFS flows, and ATProto interoperability.
4. Add an admin bootstrap path in the Docker test profile so canonical admin routes can be exercised through successful `200`/`204` flows, not only auth-gated and compatibility-shim behavior.
5. Keep OpenAPI and the PeerTube-canonical Postman collection in lockstep so future drift is caught immediately.

## Confidence Statement

Confidence is high that Athena now has a working, internally consistent validation baseline across the currently covered surface.

Confidence is also high that **full PeerTube instance import compatibility is still not complete**, because the remaining blockers are now mostly scope blockers:

- remaining compatibility-handler behavior gaps rather than missing-path gaps
- missing collaborator/plugin/runner/registration lifecycle coverage
- no true imported-instance E2E proving normal post-import behavior
- incomplete stateful coverage for Athena-only extensions outside secure messaging and livestreaming
