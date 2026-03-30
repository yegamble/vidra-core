# Feature Parity Registry

**Purpose:** Canonical list of all features. Every feature here MUST remain functional. Removing or breaking a feature triggers a stop hook.

**Reference upstream:** [PeerTube](https://github.com/Chocobozzz/PeerTube) — Vidra Core targets full API compatibility with PeerTube plus Vidra-specific extensions.

**Last Updated:** 2026-03-30

## PeerTube Parity Features (MUST maintain)

### Video Management
| Feature | Status | Package | Test File(s) | PeerTube Endpoint |
|---------|--------|---------|--------------|-------------------|
| Video CRUD | Done | `httpapi/handlers/video` | `*_test.go` | `GET/POST/PUT/DELETE /api/v1/videos/{id}` |
| Chunked upload (32MB) | Done | `httpapi/handlers/video` | `upload_test.go` | `POST /api/v1/videos/upload` |
| Video search | Done | `httpapi/handlers/video` | `search_test.go` | `GET /api/v1/search/videos` |
| Video categories | Done | `httpapi/handlers/video` | `categories_test.go` | `GET /api/v1/videos/categories` |
| Video outputs/resolutions | Done | `usecase/encoding` | `*_test.go` | `GET /api/v1/videos/{id}` (files array) |
| Thumbnails | Done | `httpapi/handlers/video` | `thumbnail_test.go` | `GET /api/v1/videos/{id}` |
| Video source download | Done | `httpapi/handlers/video` | `source_test.go` | `GET /api/v1/videos/{id}/source` |
| Video import (yt-dlp) | Done | `importer` | `*_test.go` | `POST /api/v1/videos/imports` |
| Video Studio editing | Done | `worker` | `studio_test.go` | `POST /api/v1/videos/{id}/studio/edit` |
| Podcast RSS feed | Done | `httpapi/handlers/video` | `feed_podcast_test.go` | `GET /feeds/podcast/videos.xml` |

### Transcoding
| Feature | Status | Package | Test File(s) |
|---------|--------|---------|--------------|
| H.264 transcoding | Done | `usecase/encoding` | `codec_test.go` |
| VP9 transcoding | Done | `usecase/encoding` | `codec_test.go` |
| AV1 transcoding | Done | `usecase/encoding` | `codec_test.go` |
| HLS adaptive bitrate | Done | `usecase/encoding` | `playlist_test.go` |
| Multi-resolution ladder | Done | `usecase/encoding` | `*_test.go` |
| FFmpeg worker pool | Done | `worker` | `*_test.go` |
| Encoding progress tracking | Done | `worker` | `progress_test.go` |

### User & Auth
| Feature | Status | Package | Test File(s) | PeerTube Endpoint |
|---------|--------|---------|--------------|-------------------|
| User registration | Done | `httpapi/handlers/auth` | `*_test.go` | `POST /api/v1/users/register` |
| Login (username or email) | Done | `httpapi/handlers/auth` | `*_test.go` | `POST /api/v1/users/token` |
| JWT access + refresh | Done | `middleware` | `auth_test.go` | `POST /api/v1/users/token/refresh` |
| OAuth2 (PKCE) | Done | `httpapi/handlers/auth` | `oauth_test.go` | `GET /oauth/*` |
| 2FA (TOTP) | Done | `httpapi/handlers/auth` | `twofa_test.go` | `POST /api/v1/users/me/two-factor/*` |
| User avatars | Done | `httpapi/handlers/auth` | `avatar_test.go` | `POST /api/v1/users/me/avatar/pick` |
| User profiles | Done | `httpapi/handlers/auth` | `*_test.go` | `GET /api/v1/users/me` |
| Account-based routes | Done | `httpapi/handlers/account` | `*_test.go` | `GET /api/v1/accounts/{name}` |
| User block/unblock (admin) | Done | `httpapi/handlers/admin` | `*_test.go` | `POST /api/v1/users/{id}/block` |
| User delete (admin) | Done | `httpapi/handlers/admin` | `*_test.go` | `DELETE /api/v1/admin/users/{id}` |
| User registrations workflow | Done | `httpapi/handlers/admin` | `registrations_test.go` | `GET /api/v1/admin/registrations` |
| Token sessions | Done | `httpapi/handlers/auth` | `sessions_test.go` | `GET /api/v1/users/{id}/token-sessions` |

### Channels
| Feature | Status | Package | Test File(s) | PeerTube Endpoint |
|---------|--------|---------|--------------|-------------------|
| Channel CRUD | Done | `httpapi/handlers/channel` | `*_test.go` | `GET/POST/PUT/DELETE /api/v1/channels/{id}` |
| Channel handle routes | Done | `httpapi/handlers/channel` | `channels_handle_test.go` | `GET /api/v1/video-channels/{handle}` |
| Channel avatar/banner upload | Done | `httpapi/handlers/channel` | `channel_media_test.go` | `POST /api/v1/channels/{id}/avatar` |
| Subscriptions | Done | `httpapi/handlers/channel` | `subscriptions_test.go` | `POST /api/v1/channels/{id}/subscribe` |
| Subscription batch check | Done | `httpapi/handlers/channel` | `subscriptions_exist_test.go` | `GET /api/v1/users/me/subscriptions/exist` |

### Social
| Feature | Status | Package | Test File(s) | PeerTube Endpoint |
|---------|--------|---------|--------------|-------------------|
| Comments (threaded) | Done | `httpapi/handlers/social` | `comments_test.go` | `GET/POST /api/v1/videos/{id}/comments` |
| Ratings (like/dislike) | Done | `httpapi/handlers/social` | `ratings_test.go` | `PUT /api/v1/videos/{id}/rate` |
| Playlists | Done | `httpapi/handlers/social` | `playlists_test.go` | `GET/POST /api/v1/video-playlists` |
| Playlist privacies | Done | `httpapi/handlers/social` | `playlists_privacies_test.go` | `GET /api/v1/video-playlists/privacies` |
| Captions | Done | `httpapi/handlers/social` | `captions_test.go` | `GET/POST /api/v1/videos/{id}/captions` |
| Abuse reports | Done | `httpapi/handlers/social` | `abuse_test.go` | `POST /api/v1/abuses` |
| Blocklist | Done | `httpapi/handlers/social` | `blocklist_test.go` | `GET/POST /api/v1/blocklist/*` |

### Live Streaming
| Feature | Status | Package | Test File(s) |
|---------|--------|---------|--------------|
| RTMP ingestion | Done | `livestream` | `rtmp_test.go` |
| HLS transcoding | Done | `livestream` | `hls_test.go` |
| Stream keys | Done | `livestream` | `streamkey_test.go` |
| WebSocket chat | Done | `chat` | `*_test.go` |
| Chat moderation | Done | `chat` | `moderation_test.go` |
| Stream scheduling | Done | `livestream` | `scheduler_test.go` |
| VOD conversion | Done | `livestream` | `vod_test.go` |
| Viewer tracking | Done | `livestream` | `viewer_test.go` |

### P2P Distribution
| Feature | Status | Package | Test File(s) |
|---------|--------|---------|--------------|
| WebTorrent generation | Done | `torrent` | `generator_test.go` |
| WebSocket tracker | Done | `torrent` | `tracker_test.go` |
| DHT discovery | Done | `torrent` | `dht_test.go` |
| Magnet URIs | Done | `torrent` | `*_test.go` |
| IPFS pinning | Done | `ipfs` | `*_test.go` |
| Hybrid storage (local/IPFS/S3) | Done | `storage` | `*_test.go` |

### Federation
| Feature | Status | Package | Test File(s) | PeerTube Endpoint |
|---------|--------|---------|--------------|-------------------|
| ActivityPub actors | Done | `activitypub` | `*_test.go` | `GET /api/v1/accounts/{name}` |
| ActivityPub inbox/outbox | Done | `activitypub` | `inbox_test.go` | `POST /{actor}/inbox` |
| HTTP Signatures | Done | `activitypub` | `signature_test.go` | N/A |
| WebFinger | Done | `activitypub` | `webfinger_test.go` | `GET /.well-known/webfinger` |
| NodeInfo 2.0 | Done | `activitypub` | `nodeinfo_test.go` | `GET /nodeinfo/2.0` |
| Server following API | Done | `httpapi/handlers/federation` | `server_following_test.go` | `GET /api/v1/server/following` |

### Admin
| Feature | Status | Package | Test File(s) | PeerTube Endpoint |
|---------|--------|---------|--------------|-------------------|
| Instance config | Done | `httpapi/handlers/admin` | `config_handler_test.go` | `GET/PUT /api/v1/config/custom` |
| Config reset | Done | `httpapi/handlers/admin` | `config_reset_test.go` | `DELETE /api/v1/config/custom` |
| Custom homepage | Done | `httpapi/handlers/admin` | `*_test.go` | `GET/PUT /api/v1/custom-pages/homepage/instance` |
| Instance avatar/banner | Done | `httpapi/handlers/admin` | `instance_media_test.go` | `POST/DELETE /api/v1/config/instance-avatar/pick` |
| Jobs API | Done | `httpapi/handlers/admin` | `jobs_test.go` | `GET /api/v1/admin/jobs/{state}` |

### Analytics
| Feature | Status | Package | Test File(s) |
|---------|--------|---------|--------------|
| Event collection | Done | `usecase/analytics` | `service_test.go` |
| Daily aggregation | Done | `repository` | `video_analytics_repository_test.go` |
| Retention curves | Done | `repository` | `*_test.go` |
| Channel analytics | Done | `httpapi` | `video_analytics_handlers_test.go` |

### Plugin System
| Feature | Status | Package | Test File(s) | PeerTube Endpoint |
|---------|--------|---------|--------------|-------------------|
| Plugin management | Done | `plugin` | `manager_test.go` | `GET/POST /api/v1/admin/plugins` |
| Plugin hooks | Done | `plugin` | `hooks_test.go` | N/A |
| Plugin install from URL | Done | `httpapi/handlers/plugin` | `install_test.go` | `POST /api/v1/admin/plugins/install` |
| Plugin permissions | Done | `plugin` | `*_test.go` | N/A |
| Ed25519 signatures | Done | `plugin` | `signature_test.go` | N/A |

### Other PeerTube Features
| Feature | Status | Package | Test File(s) |
|---------|--------|---------|--------------|
| Video redundancy | Done | `usecase/redundancy` | `*_test.go` |
| Notifications | Done | `httpapi` | `notification_handlers_test.go` |
| Email verification | Done | `email` | `*_test.go` |
| Setup wizard | Done | `setup` | `*_test.go` |
| Backup/restore | Done | `backup` | `*_test.go` |
| Health checks | Done | `health` | `*_test.go` |

## Vidra Core Extensions (Beyond PeerTube)

| Feature | Status | Package | Test File(s) |
|---------|--------|---------|--------------|
| IOTA payments | Done | `payments` | `*_test.go` |
| ATProto (BlueSky) | Done | `activitypub` | `atproto_test.go` |
| Secure messaging | Done | `httpapi/handlers` | `messaging_test.go` |
| IPFS streaming | Done | `ipfs` | `*_test.go` |
| Whisper transcription | Done | `whisper` | `*_test.go` |
| Migration ETL | Done | `importer` | `migration_test.go` |

## Deferred Features (Tracked, not blocking)

These are acknowledged gaps. They do NOT trigger stop hooks but should be implemented when prioritized:

| Feature | PeerTube Has It | Priority | Notes |
|---------|----------------|----------|-------|
| tus resumable upload protocol | Yes | Low | Vidra Core uses custom chunked upload |
| Video thumbnails list endpoint | Yes | Low | `GET /videos/{id}/thumbnails` |
| Video embed endpoint | Yes | Low | Currently handled by oEmbed |
| Accounts list endpoint | Yes | Low | `GET /api/v1/accounts` |
| Video password protection | Yes | Medium | Requires password field on video model |
| User data export/import (GDPR) | Yes | Medium | Compliance feature |
| Channel syncs (YouTube auto-import) | Yes | Low | Auto-import from YouTube channels |
| Storyboard generation | Yes | Low | Preview thumbnails timeline |
| Video source replacement (PUT) | Yes | Low | Replace original video file |
| External runners | Yes | Low | Vidra Core uses in-process FFmpeg |
| Full PeerTube UI client compat | Partial | Medium | Response shape parity needed |
| Fixture-based migration E2E | No | Medium | PeerTube dump migration rehearsals |

## How to Use This Registry

### Status lifecycle in autonomous mode

- `Requested` — the user asked for the feature, but implementation has not started
- `In Progress` — code, tests, or docs are actively changing
- `Done` — implementation, tests, docs, OpenAPI/Postman artifacts, and validation are all complete
- `Deferred` — acknowledged, tracked, but intentionally not in the current slice

Autonomous work should move through these statuses explicitly instead of landing "hidden" feature work outside the registry.

### When adding a feature:
1. Add entry to appropriate table above before coding starts
2. Set status to `Requested` or `In Progress` during development
3. Include the planned test file path and upstream PeerTube endpoint when applicable
4. Set to `Done` only after tests pass, docs are updated, OpenAPI/Postman changes land, and `make validate-all` succeeds

### When modifying a feature:
1. Find the entry in this registry
2. Run its existing tests BEFORE making changes
3. Verify tests still pass AFTER changes
4. If the behavior is PeerTube-facing, confirm the upstream behavior still matches
5. If test file path changed, update registry

### When a stop hook fires:
1. Check which feature was affected
2. Verify the feature still works (run its tests)
3. Verify the registry status is still accurate (`Requested`, `In Progress`, `Done`, or `Deferred`)
4. If broken, revert and fix
5. If intentional change, update this registry with user approval

### Periodic audit (monthly):
1. Run `make test` and verify all features in registry still pass
2. Compare against upstream PeerTube releases for new features
3. Update deferred features list if priorities change
4. Verify OpenAPI specs match implementation: `make verify-openapi`
