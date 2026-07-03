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

- Supported `migrationVersion` range: **700 – 900** (constants
  `MinSupportedSchemaVersion` / `MaxSupportedSchemaVersion` in
  `internal/peertubeimport/version.go`).
- This range approximately covers PeerTube's **5.x – 6.x** schema line
  (2023–2024). It is deliberately conservative and APPROXIMATE: the exact
  version↔release mapping is not authoritatively pinned here, so an operator
  migrating from a version near the edges should verify column compatibility by
  hand before `--force`.
- The importer reads a documented SUBSET of PeerTube's schema (user/account/
  actor, videoChannel, video/videoFile/thumbnail/videoCaption, videoComment,
  videoPlaylist/element, tag/videoTag, actorFollow) plus `application`. HLS
  streaming playlists, moderation state, notification settings, and watch history
  are intentionally deferred (regenerate/reconcile post-import).
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
