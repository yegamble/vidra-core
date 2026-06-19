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
