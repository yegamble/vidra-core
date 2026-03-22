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
- Stateful Newman execution is real and now passes end to end
- Runtime federation discovery is mounted in the validated container runtime
- Missing-video and missing-import error paths now return PeerTube-style `404`/`400` semantics instead of `500`
- Channel subscription and livestream flows now pass their real stateful E2E paths

What is not true yet:

- Some PeerTube-canonical endpoint families are still absent or Athena-shaped
- There is still no true “import an existing PeerTube instance and use it normally” E2E scenario
- Athena-only extensions such as crypto payments, IPFS-native flows, and ATProto interoperability are not yet proven by the stateful Newman suite

Current Newman result:

- Collections run: 12
- Passed: 12
- Failed: 0
- Passing collections: `athena-auth`, `athena-videos`, `athena-uploads`, `athena-channels`, `athena-instance-config`, `athena-imports`, `athena-feeds`, `athena-blocklist`, `athena-notifications`, `athena-livestreaming`, `athena-federation`, `athena-secure-messaging`

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

## Highest-Risk PeerTube Compatibility Gaps

These are the gaps most likely to block a true PeerTube-instance import or an external PeerTube client/tooling integration even though the current Athena validation suite is green.

| Gap | Evidence | Why it matters |
| --- | --- | --- |
| PeerTube-canonical admin and maintenance paths are still incomplete | Route/spec audit still shows missing or Athena-shaped `registrations`, `jobs`, `plugins`, `runners`, and import lifecycle aliases | Imported tooling expecting exact PeerTube routes will still break |
| Handle/collaborator import stories are incomplete | `/api/v1/video-channels/{channelHandle}/collaborators*` is still absent | Multi-user channel administration is part of real PeerTube instance behavior |
| True import/federation continuation is not E2E-proven | The current suite validates discovery endpoints and import CRUD, but not a full imported remote instance behaving normally afterward | “Drop-in PeerTube instance target” requires more than route availability |
| Athena-only extensions are only partially validated | Secure messaging and livestreaming now pass; crypto payments, IPFS-native flows, and ATProto interoperability are not in the stateful suite | Extra Athena features still need proof beyond route presence |

PeerTube-canonical path gaps still visible in the route/spec comparison:

- `/api/v1/users/registrations*` is not exposed at PeerTube’s path
- `/api/v1/jobs/{state}` is not exposed at PeerTube’s path
- `/api/v1/plugins*` remains Athena-admin-shaped rather than PeerTube-canonical
- `/api/v1/video-channels/{channelHandle}/collaborators*` is not present
- `/api/v1/videos/imports/{id}/cancel` and `/api/v1/videos/imports/{id}/retry` aliases are not present
- `/api/v1/runners*` is still absent

## Missing or Incomplete User Stories

The following user stories are not yet fully satisfied end to end:

- Import a PeerTube instance and continue normal follow/watch/discovery behavior without Athena-specific adaptation
- Import PeerTube channels that rely on handle-based discovery and collaborator management
- Retry or cancel imported videos using PeerTube’s canonical import lifecycle endpoints
- Administer PeerTube-style registration approvals through PeerTube-compatible registration moderation routes
- Use plugin and runner management with PeerTube-compatible endpoints

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

The remaining OpenAPI/parity risk is mostly scope risk: missing PeerTube-canonical endpoint families and missing import-scenario coverage.

## Recommended Next Steps

1. Add the remaining PeerTube-canonical aliases and paths:
   `users/registrations`, `jobs/{state}`, `plugins`, `runners`, import `cancel`/`retry`, channel collaborators.
2. Add a true “import PeerTube instance” E2E scenario that verifies:
   discovery, remote actor resolution, channel mapping, imported videos, and follow/watch behavior after import.
3. Extend stateful coverage for Athena-only extensions:
   crypto payments, IPFS flows, and ATProto interoperability.
4. Split Postman collections into:
   PeerTube-compat collections and Athena-extension collections.
5. Continue tightening OpenAPI around PeerTube-canonical alias coverage so route gaps are visible at spec-review time.

## Confidence Statement

Confidence is high that Athena now has a working, internally consistent validation baseline across the currently covered surface.

Confidence is also high that **full PeerTube instance import compatibility is still not complete**, because the remaining blockers are now mostly scope blockers:

- unresolved canonical-route gaps
- missing collaborator/plugin/runner/registration lifecycle coverage
- no true imported-instance E2E proving normal post-import behavior
- incomplete stateful coverage for Athena-only extensions outside secure messaging and livestreaming
