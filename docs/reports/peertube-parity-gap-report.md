# PeerTube Compatibility Validation Report

Generated: 2026-03-23
PeerTube references:
- https://github.com/chocobozzz/PeerTube
- https://docs.joinpeertube.org/api-rest-reference.html
- https://docs.joinpeertube.org/maintain/migration

## Scope

This report documents the current validation-driven status of Athena's PeerTube-aligned API/runtime surface and the remaining gaps for full PeerTube instance migration.

Evidence used in this pass:

- Athena route and handler audit in `internal/httpapi/routes.go`
- Athena OpenAPI validation and code generation
- Full short Go verification: `env -u GOROOT go test -short ./...`
- Focused social/repository/ATProto regression suites
- Containerized Newman execution against the Docker `test` profile
- Review of Athena migration docs versus PeerTube's upstream REST and migration documentation

## Bottom Line

Athena now has a **green validation baseline for the PeerTube-shaped API/runtime surface** plus Athena-only extensions. OpenAPI verifies cleanly, Go tests pass, and all 19 selected Newman collections pass against the live Docker test stack.

What is not true yet:

- Athena does **not** ship a real end-to-end PeerTube instance importer/ETL that can ingest a PeerTube PostgreSQL dump plus media storage layout and migrate it automatically.
- Athena does **not** yet run fixture-based conformance tests against a live upstream PeerTube instance or the real PeerTube web/mobile clients.

So the accurate statement today is:

- **API/runtime parity confidence:** high
- **"Import a real PeerTube instance and have it just work" confidence:** medium, because the migration tooling itself is still missing

## What Is Proven Today

- `api/openapi.yaml` validates cleanly and the generated types build
- `./scripts/verify-openapi.sh` passes
- `env -u GOROOT go test -short ./...` passes
- Focused ATProto/social/repository regressions pass
- The Docker E2E stack boots and the stateful Newman suite passes end to end
- All 19 selected Newman collections pass with live server assertions against the Docker `test` profile
- PeerTube-style import lifecycle routes work with PeerTube-style aliases such as `targetUrl`, `channelId`, `video.privacy`, `video.category`, `count`, and `start`
- PeerTube-canonical admin routes for registrations, jobs, plugins, collaborators, runner tokens, and runner lifecycle are mounted and validated
- ActivityPub discovery and instance follow lifecycle are proven end to end through WebFinger, NodeInfo, and server follow/unfollow flows
- Athena-only extensions are also green in the same validation pass: secure messaging, IOTA payments, IPFS, livestreaming, and ATProto social flows

## Current Newman Result

- Collections registered in the stateful suite: 19
- Validated against live server: 19
- Failed: 0

Passing collections:

- `athena-auth`
- `athena-videos`
- `athena-uploads`
- `athena-channels`
- `athena-instance-config`
- `athena-imports`
- `athena-peertube-canonical`
- `athena-feeds`
- `athena-blocklist`
- `athena-notifications`
- `athena-livestreaming`
- `athena-federation`
- `athena-secure-messaging`
- `athena-ipfs`
- `athena-runners`
- `athena-plugins`
- `athena-payments`
- `athena-import-lifecycle`
- `athena-atproto`

## PeerTube-Aligned Capability Status

Covered and live-validated:

- Auth bootstrap and instance config
- Video CRUD, search, uploads, and stream-related routes
- Channels and subscriptions
- Public feeds and subscription feeds
- Import lifecycle and PeerTube-canonical import/admin aliases
- Registrations, jobs, plugins, and runners
- Notifications and blocklists
- ActivityPub discovery and server follow/unfollow lifecycle

Covered as Athena extensions and validated in the same environment:

- Secure messaging
- IOTA-based payments
- IPFS metrics/gateway surfaces
- ATProto social graph, likes, comments, moderation labels, and feed ingest

## Remaining Capability Gaps

These are the main remaining gaps if the goal is "import a PeerTube instance into Athena and have it behave as usual":

1. No shipped PeerTube ETL/importer

- There is no production migration command or service that restores a PeerTube dump into staging, maps PeerTube tables into Athena tables, copies media, and verifies the import automatically.

2. No fixture-backed migration rehearsal

- Athena does not yet include a canonical PeerTube export fixture plus an automated Athena import test that proves users, channels, videos, comments, playlists, captions, subscriptions, and metadata survive migration.

3. No live upstream client compatibility run

- Athena validates its own contract well, but it does not yet run the PeerTube web UI, mobile app, or a live upstream PeerTube instance against Athena as a compatibility oracle.

4. Some contract differences still exist

- Response envelopes and pagination styles are still mixed by handler family instead of being strict one-to-one PeerTube clones everywhere.

## Missing User Stories

These user stories are still missing or only partially covered:

1. As an Athena operator, I can import a PeerTube database dump and media store into Athena with a supported migration command.
2. As an Athena operator, I can run a dry-run migration and get a report of unmapped entities, invalid rows, and media copy failures before cutover.
3. As a migrated user, my channels, videos, comments, playlists, captions, subscriptions, and watch history behave the same after migration.
4. As an admin, I can verify a migrated instance with an automated post-import validation suite instead of manual spot checks.
5. As QA, I can run a compatibility suite against a real PeerTube reference instance or client to detect schema and behavior drift.

## OpenAPI Status

OpenAPI is aligned with the current Athena runtime for the validated surface:

- `api/openapi.yaml` validates
- `api/openapi_social.yaml` documents the social/ATProto-related additions exercised in Postman
- `api/openapi_payments.yaml` reflects the current wallet/payment intent semantics
- `api/openapi_ipfs.yaml` now documents the IPFS routes exercised by Newman

This means Athena's documented contract is consistent with the live test stack for the routes exercised in the 19-collection suite.

## Confidence Statement

**Confidence is high that Athena's validated API/runtime surface is healthy and substantially PeerTube-aligned.**

**Confidence is not yet high that a real PeerTube instance can be imported into Athena "as usual" without additional engineering**, because the actual migration/ETL tooling and fixture-based migration rehearsal are still missing.

## Recommended Next Steps

1. Build a real PeerTube import pipeline: PeerTube dump restore -> Athena staging transform -> media copy/reindex -> validation report.
2. Add a fixture-based migration E2E that proves the full PeerTube-to-Athena data path on representative sample content.
3. Add one upstream compatibility harness using either a real PeerTube reference instance or PeerTube client fixtures to catch contract drift.
