# Postman Test Collections

This directory contains Postman collections for comprehensive API testing of the Athena PeerTube-compatible backend.

## Quick Start

### 1. Install Newman (Postman CLI)

```bash
npm install -g newman
```

### 2. Run All Collections

```bash
cd postman
./run-all-tests.sh                              # Uses athena.local.postman_environment.json
./run-all-tests.sh my-env.json                  # Use a different environment file
```

### 3. Run Individual Collection

```bash
newman run athena-auth.postman_collection.json -e athena.local.postman_environment.json
```

---

## Collections Overview

### Core API Collections (in CI runner)

| # | Collection | Requests | Purpose | In CI |
|---|-----------|----------|---------|-------|
| 1 | **athena-auth** | 61 | Authentication, avatar uploads, user CRUD | Yes |
| 2 | **athena-videos** | -- | Video CRUD, search, upload, stream | Yes |
| 3 | **athena-uploads** | 11 | Chunked uploads, resume, encoding status | Yes |
| 4 | **athena-channels** | -- | Channel CRUD, subscribe/unsubscribe | Yes |
| 5 | **athena-social** | -- | Social features (follows, ratings) | Yes |
| 6 | **athena-playlists** | -- | Playlist CRUD, items, reorder | Yes |
| 7 | **athena-instance-config** | -- | Public config and quota endpoints | Yes |
| 8 | **athena-imports** | 10 | External video imports | Yes |
| 9 | **athena-peertube-canonical** | -- | PeerTube-canonical registrations, jobs, plugins | Yes |
| 10 | **athena-feeds** | -- | Public and subscription feeds (RSS/Atom) | Yes |
| 11 | **athena-blocklist** | -- | Account/server blocklist state transitions | Yes |
| 12 | **athena-moderation** | -- | Abuse reports, blacklist, content moderation | Yes |
| 13 | **athena-notifications** | -- | Notification list/read/delete | Yes |
| 14 | **athena-livestreaming** | -- | Stream create/get/stats/session/channel | Yes |
| 15 | **athena-federation** | -- | WebFinger, NodeInfo, federation discovery | Yes |
| 16 | **athena-secure-messaging** | -- | Encrypted messaging E2EE flow | Yes |
| 17 | **athena-ipfs** | -- | IPFS metrics and gateway health | Yes |
| 18 | **athena-runners** | 24 | Runner registration, job lifecycle, file upload | Yes |
| 19 | **athena-plugins** | 13 | Plugin discovery, settings, install contract | Yes |
| 20 | **athena-payments** | 14 | IOTA wallet lifecycle, payment intents | Yes |
| 21 | **athena-import-lifecycle** | 14 | Import create->list->cancel->retry lifecycle | Yes |
| 22 | **athena-atproto** | 21 | ATProto social: actors, follows, likes, comments | Yes |
| 23 | **athena-chapters-blacklist** | -- | Video chapters and blacklist management | Yes |
| 24 | **athena-analytics** | 13 | View tracking, analytics, trending | Yes |
| 25 | **athena-encoding-jobs** | -- | Encoding job status and management | Yes |
| 26 | **athena-captions** | 8 | Video captions: list, upload (SRT/VTT), delete, auto-generate | Yes |
| 27 | **athena-2fa** | 8 | Two-factor authentication lifecycle | Yes |
| 28 | **athena-chat** | 8 | Live stream chat: messages, moderators, bans | Yes |
| 29 | **athena-redundancy** | 10 | Instance peers, redundancy policies, stats | Yes |
| 30 | **athena-watched-words** | 8 | Server watched words and auto-tag policies | Yes |
| 31 | **athena-video-passwords** | 6 | Password-protected video access | Yes |
| 32 | **athena-user-import-export** | 6 | User data export/import portability | Yes |
| 33 | **athena-channel-sync** | 4 | Channel sync with external feeds | Yes |
| 34 | **athena-player-settings** | 4 | Video player configuration | Yes |
| 35 | **athena-admin-debug** | 6 | Admin debug info, instance stats, user/video admin | Yes |
| 36 | **athena-video-studio** | 6 | Video studio editing jobs (create, list, status) | Yes |
| 37 | **athena-migration-etl** | 7 | PeerTube migration import pipeline | Yes |
| 38 | **athena-registration-edge-cases** | -- | Username length limits, special chars, security tests | Yes |
| 39 | **athena-edge-cases-security** | -- | Additional security edge case testing | Yes |
| 40 | **athena-virus-scanner-tests** | -- | Virus scanner integration tests | Yes |
| 41 | **athena-frontend-api-gaps** | -- | Frontend API gap coverage | Yes |

### E2E Workflow Chain Collections (in CI runner)

| # | Collection | Requests | Purpose | In CI |
|---|-----------|----------|---------|-------|
| 42 | **athena-e2e-auth-flow** | 6 | Register -> verify -> login -> 2FA -> logout -> re-login | Yes |
| 43 | **athena-e2e-video-lifecycle** | 8 | Login -> channel -> video -> caption -> rate -> comment | Yes |
| 44 | **athena-e2e-payment-flow** | 7 | Register users -> wallets -> payment intent -> history | Yes |

---

## Environment Variables

All collections use `athena.local.postman_environment.json`:

```json
{
  "baseUrl": "http://app-test:8080",
  "access_token": "",
  "refresh_token": "",
  "user_id": "",
  "username": "",
  "email": "",
  "password": ""
}
```

The `access_token` is automatically set after successful login in the auth collection. Environment is exported between collections in the CI runner, so tokens and IDs flow through the full suite.

Additional variables set during runs include: `admin_access_token`, `video_id`, `channel_id`, `upload_session_id`, `import_job_id`, `viewer_fingerprint`, `runner_token`, `caption_id`, `twofa_secret`, and many more.

---

## CI Runner (`run-all-tests.sh`)

The runner executes all 44 collections in a stateful sequence:

1. Creates a working copy of the environment file
2. Runs each collection with Newman, exporting environment between runs
3. Reports pass/fail summary with failed collection names
4. Exits with code 1 if any collection fails

```bash
cd postman
./run-all-tests.sh                  # Default environment
./run-all-tests.sh my-env.json      # Custom environment
```

### GitHub Actions Example

```yaml
- name: Run Postman Tests
  run: |
    npm install -g newman
    cd postman
    ./run-all-tests.sh athena.local.postman_environment.json
```

---

## Test Coverage Summary

| Category | Collections | Coverage |
|----------|------------|----------|
| **Authentication** | auth, 2fa, e2e-auth-flow | Register, login, logout, token refresh, 2FA lifecycle |
| **Video Content** | videos, uploads, captions, video-passwords, encoding-jobs | CRUD, chunked upload, captions, password protection |
| **Channels** | channels, channel-sync, playlists | CRUD, subscriptions, external sync, playlists |
| **Social** | social, notifications, comments | Follows, ratings, comments, notifications |
| **Moderation** | blocklist, moderation, watched-words, chapters-blacklist | Abuse reports, blocklists, word filters, blacklist |
| **Streaming** | livestreaming, chat | Stream lifecycle, chat messages, moderators, bans |
| **Federation** | federation, atproto, redundancy | ActivityPub, ATProto, WebFinger, redundancy policies |
| **Infrastructure** | instance-config, admin-debug, ipfs, player-settings | Config, admin ops, IPFS metrics, player settings |
| **Payments** | payments, e2e-payment-flow | IOTA wallets, payment intents, transaction history |
| **Import/Export** | imports, import-lifecycle, user-import-export | Video imports, user data portability |
| **Video Editing** | video-studio | Server-side video editing jobs and FFmpeg pipeline |
| **Migration** | migration-etl | PeerTube dump import, dry-run, cancel |
| **Runners** | runners, encoding-jobs | Remote runner lifecycle, encoding job management |
| **Security** | secure-messaging, registration-edge-cases | E2EE messaging, input validation, SSRF protection |
| **E2E Workflows** | e2e-auth-flow, e2e-video-lifecycle, e2e-payment-flow | Full user journeys across multiple API surfaces |
| **Compatibility** | peertube-canonical, feeds, frontend-api-gaps | PeerTube API compatibility, RSS/Atom feeds |

**Total**: 44 collections in CI, covering all major API surfaces.

---

## Security Testing Highlights

- SSRF protection (blocks private IPs, localhost, RFC1918)
- SQL injection and XSS prevention in registration
- Magic byte validation for file uploads
- Executable file rejection
- Token expiration and refresh
- Rate limiting enforcement
- Role-based access control (admin vs user)
- Password-protected video access
- E2EE messaging key exchange

---

## Adding New Collections

1. Create `athena-<name>.postman_collection.json` following existing patterns
2. Use `{{baseUrl}}` for the base URL (not `{{base_url}}`)
3. Use `pm.environment.set()` to share state between requests
4. Add the collection to `run-all-tests.sh` in the appropriate position
5. Update this README with the new collection details
