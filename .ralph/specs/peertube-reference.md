# PeerTube Reference Survey

This file pins the PeerTube behavior baseline used for Vidra parity work.

## Current reference baseline

- Reference status: INCOMPLETE_SURVEY
- PeerTube version/API version: TBD by Ralph at first survey loop
- Survey date: TBD
- Surveyed by: Ralph
- Official docs: https://docs.joinpeertube.org/
- REST API reference: https://docs.joinpeertube.org/api-rest-reference.html
- ActivityPub reference: https://docs.joinpeertube.org/api/activitypub
- Plugin/theme API reference: https://docs.joinpeertube.org/api/plugins
- Demo/local instance inspected: TBD
- Screenshot/trace evidence location: `.ralph/docs/generated/parity-evidence/`

## PeerTube import — pinned supported schema versions (fix_plan P18)

The one-way PeerTube→Vidra import tool (`.ralph/specs/peertube-import.md`,
`internal/peertubeimport`, `cmd/peertube-import`) detects the source schema
version at preflight by reading the integer `application.migrationVersion`
column and REFUSES to run outside the verified range unless a human passes
`--force`. Ralph/agents MUST NEVER self-pass `--force` — an unverified version is
a hard stop requiring operator sign-off.

- Supported `migrationVersion` range: **700 – 1040** (constants
  `MinSupportedSchemaVersion` / `MaxSupportedSchemaVersion` in
  `internal/peertubeimport/version.go`).
- The upper bound is pinned to **PeerTube v8.2.4**, commit
  `30eb3cc4f8701198ec1c785aedd2fbb5db401a2f`: its
  [LAST_MIGRATION_VERSION](https://github.com/Chocobozzz/PeerTube/blob/30eb3cc4f8701198ec1c785aedd2fbb5db401a2f/server/core/initializers/constants.ts)
  is 1040. Verified on 2026-09-17. The older lower bound remains unchanged.
- Reviewed upstream migrations 1005–1040: download statistics rename videoView
  to videoStat (neither is imported); live DVR and ownership notifications add
  or rename unimported fields; playlist thumbnails lose their unique index
  (video artwork selection excludes playlist-only rows); varchar widening
  preserves reader types; 1040 expires OAuth tokens (never imported).
- PostgreSQL end-to-end coverage runs both schema 800 and 1040, including
  current actor-side account/channel links, unified thumbnails, duplicate
  playlist thumbnail variants, dry-run, import, media copying, and idempotent
  reruns. Unknown, older, and newer-than-1040 schemas remain gated.
- The importer reads a subset of the source, not every PeerTube feature. HLS
  copy/reference, local-video watch history and artwork are supported. Missing
  source artwork is distinct from schema incompatibility: HTTP 404/410 after
  storage fallback is reported as missing_source within failed counts and
  remains retryable after the source file is restored. Moderation/notification
  settings, remote watch history, live sessions and plugin runtime state still
  have coverage gaps listed in each import report.
- When the reference release is bumped, re-verify this range against a known
  PeerTube dump and update the constants + this note in the same change.

## Survey rules

1. Use PeerTube as behavioral reference only.
2. Do not copy PeerTube source code, proprietary assets, translations, screenshots, branding, or exact visual styling.
3. Record behaviors, states, routes, controls, permissions, APIs, and acceptance criteria.
4. Update this file when the reference version changes.
5. Do not chase a moving target mid-build. Version bumps require a new parity refresh task.

## Known initial source areas to survey

- Use web: watch/share/download, setup account, user library, publish upload/live, studio quick edit, video statistics, channel sync, search, mute, report, accessibility, third-party apps.
- Use mobile: app onboarding, platforms tab, watch videos, library/watch later/history/downloaded videos.
- Admin: users/auth, moderation, configuration, federation, jobs, runners, plugins/themes, logs, storage/transcoding settings.
- API: REST OpenAPI, REST quick start, ActivityPub, player embed API, plugins/themes API, NodeInfo/instance discovery.

## Known survey gaps

- [ ] Exact latest PeerTube release/API version pinned.
- [ ] Live/demo instance inspected for button-level UI.
- [ ] OpenAPI downloaded and endpoint inventory generated.
- [ ] Admin UI page map completed.
- [ ] Mobile/responsive behavior compared.
- [ ] Plugin/theme boundary mapped to Vidra equivalent extension policy.
