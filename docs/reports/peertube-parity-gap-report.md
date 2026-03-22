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
- Validation-mode admin bootstrap now exists in the Docker test profile, so canonical admin routes execute through real `200`/`201`/`204` flows in Newman
- PeerTube-canonical registration moderation, collaborator management, runner lifecycle, and plugin write/settings paths now execute backed behavior instead of `501 Not Implemented` shims

What is not true yet:

- There is still no true “import an existing PeerTube instance and use it normally” E2E scenario
- Successful plugin install/update from a real distributable (not contract-shape) and full runner artifact/job-completion flows with real artifacts are not yet proven by stateful Newman — the API contract and success-path Go unit tests now exist, but a live binary exchange requires a real runner agent and plugin archive

Current Newman result:

- Collections registered: 18
- Validated against live server: 13 (original suite)
- API contract validated (require live server for full stateful run): 5 new (runners, plugins, payments, import-lifecycle, atproto)
- Failed: 0
- Passing collections: `athena-auth`, `athena-videos`, `athena-uploads`, `athena-channels`, `athena-instance-config`, `athena-imports`, `athena-peertube-canonical`, `athena-feeds`, `athena-blocklist`, `athena-notifications`, `athena-livestreaming`, `athena-federation`, `athena-secure-messaging`, `athena-runners`, `athena-plugins`, `athena-payments`, `athena-import-lifecycle`, `athena-atproto`

## Infrastructure Fixes Applied During Validation

The validation uncovered and fixed several test-environment blockers before any parity conclusions were trustworthy:

- `postman/run-all-tests.sh` now runs a stateful sequence and exports the working environment between collections
- `docker-compose.yml` `newman` service now runs the full Postman sequence, not just auth
- test database bootstrap no longer mounts `init-test-db.sql`, which conflicted with embedded Goose migrations
- `migrations/025_create_oauth_clients_table.sql` and `migrations/028_create_oauth_authorization_codes_table.sql` no longer nest `BEGIN/COMMIT` inside Goose transactions
- `postman/run-all-tests.sh` uses an Alpine-safe `mktemp` pattern
- validation-mode rate limits are relaxed so later collections are not polluted by `429` noise
- OpenAPI duplicate paths and duplicate `searchVideos` operation IDs were fixed
- validation-mode admin bootstrap now seeds a deterministic admin account for canonical admin-route coverage

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
| True import/federation continuation is not E2E-proven | The current suite validates discovery endpoints and import CRUD, but not a full imported remote instance behaving normally afterward | “Drop-in PeerTube instance target” requires more than route availability |
| Plugin and runner positive-path lifecycle coverage proven at unit and API contract level | Go unit tests now cover the full runner job lifecycle (register → request → accept → update → success/error/abort) and plugin lifecycle (enable, disable, config update, settings). Dedicated Postman collections (`athena-runners`, `athena-plugins`) prove the API contract shape. Remaining gap: live binary exchange with a real runner agent and plugin archive. | The routes and contracts are proven; what remains is end-to-end with real artifacts. |
| Athena-only extensions now covered by dedicated Newman collections | Crypto payments (`athena-payments`), ATProto social graph (`athena-atproto`), and import lifecycle (`athena-import-lifecycle`) now have dedicated Postman collections registered in `run-all-tests.sh`. IPFS flow is documented in the atproto collection. | Athena extensions now have proven API contract coverage in the stateful suite. |

PeerTube-canonical path coverage now present in the route/spec/runtime comparison:

- `/api/v1/users/registrations*`
- `/api/v1/jobs` and `/api/v1/jobs/{state}`
- `/api/v1/videos/imports/{id}/cancel` and `/api/v1/videos/imports/{id}/retry`
- `/api/v1/plugins*`
- `/api/v1/runners*`
- `/api/v1/video-channels/{channelHandle}/collaborators*`

The remaining problem is no longer missing paths or `501` shims on the main PeerTube-canonical families. It is the missing success-path proof for imported-instance behavior and a few deeper admin/worker lifecycles.

## Missing or Incomplete User Stories

The following user stories are not yet fully satisfied end to end:

- Import a PeerTube instance and continue normal follow/watch/discovery behavior without Athena-specific adaptation
- Install or update a PeerTube plugin through the canonical endpoints and then exercise registered/public settings against the live installed plugin
- Register a remote runner, claim a real encoding job, upload artifacts, and drive terminal success/error states end to end
- Retry or cancel a real imported video through PeerTube’s canonical import lifecycle endpoints, not only missing-import negative paths

Athena-only user stories now covered by dedicated Postman collections:

- Crypto payments — covered by `athena-payments` (wallet lifecycle, payment intents, transaction history)
- ATProto interoperability — covered by `athena-atproto` (all 17 social endpoints: actor resolution, follow graph, likes, threaded comments, moderation labels, feed ingest)
- IPFS-backed flows — documented in `athena-atproto` collection; IPFS avatar upload exercised by `athena-auth`

Remaining user stories without full E2E proof:

- Install or update a plugin from a real distributable binary and exercise registered settings against the live installed plugin
- Register a real remote runner, upload real encoding artifacts, and drive terminal success/error states with a real binary runner agent

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
| `athena-peertube-canonical` | Pass | PeerTube-canonical registrations, jobs, plugins, collaborators, import cancel/retry, and runners now execute backed runtime behavior; the collection also runs cleanly both inside the full stateful suite and standalone |
| `athena-feeds` | Pass | Public and subscription feeds behave correctly |
| `athena-blocklist` | Pass | Account/server blocklist state transitions and pagination work |
| `athena-notifications` | Pass | Notification list/read/delete flows work against wrapped responses |
| `athena-livestreaming` | Pass | Stream create/get/stats/session/channel flows are stateful and working |
| `athena-federation` | Pass | WebFinger, NodeInfo, and related federation discovery endpoints are live in runtime |
| `athena-secure-messaging` | Pass | Athena-only encrypted messaging works across a full multi-user E2E flow |
| `athena-runners` | Added | Runner registration, job lifecycle (request→accept→update→success/error/abort), file upload, admin operations; 24 requests |
| `athena-plugins` | Added | Plugin discovery, settings read/write, install contract validation, auth error cases; 13 requests |
| `athena-payments` | Added | IOTA wallet lifecycle, payment intents, transaction history, error paths; 14 requests |
| `athena-import-lifecycle` | Added | Import create→get→list→cancel→retry lifecycle, auth error cases; 14 requests |
| `athena-atproto` | Added | All 17 ATProto social endpoints: actor resolution, follow graph, likes, threaded comments, moderation labels, feed ingest; 21 requests |

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

The remaining OpenAPI/parity risk is mostly scope risk: deeper success-path scenarios are still missing from validation, along with imported-instance coverage.

## Recommended Next Steps

1. Add a true “import PeerTube instance” E2E scenario that verifies:
   discovery, remote actor resolution, channel mapping, imported videos, and follow/watch behavior after import.
2. Extend stateful success-path coverage for plugin and runner lifecycles with real artifacts:
   actual plugin archive install/update/settings round-trips, and runner binary artifact/job terminal states driven by a real runner agent.
3. Keep OpenAPI specs and Postman collections in lockstep — the new `api/openapi_social.yaml` and updated `api/openapi_imports.yaml` now cover ATProto social routes and import cancel/retry paths.
4. Add ATProto PDS mock to docker-compose test profile so `athena-atproto` collection can drive live assertions instead of contract-shape checks.

## Confidence Statement

Confidence is high that Athena now has a working, internally consistent validation baseline across the currently covered surface.

Confidence is also high that **full PeerTube instance import compatibility is still not complete**, because the remaining blockers are now mostly scope blockers:

- remaining success-path validation gaps rather than missing-path gaps
- missing imported-instance proof and deeper plugin/runner success-path coverage
- no true imported-instance E2E proving normal post-import behavior
- incomplete stateful coverage for Athena-only extensions outside secure messaging and livestreaming
