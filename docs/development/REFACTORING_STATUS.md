# HTTPAPI Reorganization - In Progress

**Status:** Phase 1 ~90% Complete
**Date:** October 26, 2025

## What's Been Completed

### ✅ Phase 1: Directory Reorganization (90% Complete)

**File Movement - COMPLETE:**

- ✅ Created 10 handler subdirectories
- ✅ Moved all 88 handler files from flat structure to organized packages
- ✅ Moved response.go and helpers.go to shared/ package
- ✅ Moved dependencies.go to shared/ package

**Package Updates - COMPLETE:**

- ✅ Updated all package declarations (httpapi → domain-specific names)
- ✅ Updated method receivers (*Server →*DomainHandlers)
- ✅ Updated field references (s. → h.)
- ✅ Added shared package imports to all handlers
- ✅ Updated WriteJSON, WriteError calls to use shared prefix

**Handler Structs Created:**

- ✅ AuthHandlers (auth/)
- ✅ VideoHandlers (video/)
- ⚠️ ChannelHandlers, SocialHandlers, LivestreamHandlers, etc. (structs created but need dependency review)

### Current Structure

```
internal/httpapi/
├── handlers/
│   ├── auth/           (10 files) - OAuth, login, registration, email verification, users
│   ├── video/          (25 files) - Videos, uploads, encoding, analytics, views, HLS, imports
│   ├── channel/        (6 files)  - Channels, subscriptions
│   ├── social/         (8 files)  - Comments, ratings, playlists, captions
│   ├── livestream/     (4 files)  - RTMP, HLS, waiting rooms
│   ├── federation/     (9 files)  - ActivityPub, admin federation, redundancy
│   ├── moderation/     (3 files)  - Content moderation
│   ├── messaging/      (11 files) - Messages, chat, notifications
│   ├── plugin/         (1 file)   - Plugin system
│   └── admin/          (1 file)   - Instance management
├── shared/
│   ├── response.go     - WriteJSON, WriteError, WriteJSONWithMeta
│   ├── helpers.go      - Shared utility functions
│   └── dependencies.go - HandlerDependencies struct
├── handlers.go         - Old Server struct (keep for reference)
├── health.go           - Health check handlers
└── routes.go           - Route registration (NEEDS UPDATE)
```

## What Needs to Be Done

### 🚧 Phase 1 Remaining (10% - Critical)

#### 1. Update routes.go (CRITICAL)

**Current State:** routes_refactored.go renamed to routes.go but still uses old Server struct

**Required Changes:**

1. Remove dependency on old Server struct
2. Instantiate all new handler structs:

   ```go
   authHandlers := auth.NewAuthHandlers(...)
   videoHandlers := video.NewVideoHandlers(...)
   channelHandlers := channel.NewChannelHandlers(...)
   // etc.
   ```

3. Update all route registrations to use new handlers:

   ```go
   // Old:
   r.Post("/auth/login", server.Login)

   // New:
   r.Post("/auth/login", authHandlers.Login)
   ```

**Files to Update:**

- `internal/httpapi/routes.go` - Complete rewrite needed

#### 2. Review and Fix Handler Constructors

Some handler structs may have incorrect dependencies. Need to:

- Review each handlers.go file in subdirectories
- Ensure all required dependencies are included
- Match constructor parameters to actual handler method usage

**Files to Review:**

- `handlers/channel/handlers.go`
- `handlers/social/handlers.go`
- `handlers/livestream/handlers.go`
- `handlers/federation/handlers.go`
- `handlers/moderation/handlers.go`
- `handlers/messaging/handlers.go`
- `handlers/plugin/handlers.go`
- `handlers/admin/handlers.go`

#### 3. Fix Method Signatures

Some handlers may have methods that don't match handler struct:

- Search for remaining `*Server` receivers
- Update to use correct handler struct
- Ensure field names match struct definition

#### 4. Test Compilation

```bash
go build ./internal/httpapi/...
```

Expected errors:

- Import cycle errors (if any)
- Missing fields in handler structs
- Undefined methods/functions
- Type mismatches

### Phase 2: Integration Tests (Partially Complete)

- Move 16 integration test files to `/tests/integration/`
- ✅ ~~Move test fixtures from `internal/httpapi/storage/` to `/tests/fixtures/`~~ - Removed `/internal/httpapi/storage/`; tests now use `/storage/` at project root
- Update test package declarations
- Update imports in test files

### Phase 3: Repository Tests (Not Started)

- Create video_repository_test.go
- Create playlist_repository_test.go
- Create caption_repository_test.go
- Create oauth_repository_test.go
- Target: 40%+ coverage

## Quick Reference: Handler Methods

### AuthHandlers

- Login, Register, Logout
- RefreshToken
- OAuthToken, OAuthAuthorize, OAuthRevoke, OAuthIntrospect
- Email verification methods
- User CRUD (from users.go, avatar.go)

### VideoHandlers

- ListVideos, GetVideo, CreateVideo, UpdateVideo, DeleteVideo
- SearchVideos
- UploadVideoFile, VideoUploadChunk, VideoCompleteUpload
- Encoding status and management
- Analytics and views
- HLS streaming
- Video imports
- Categories

### ChannelHandlers

- Channel CRUD
- Subscriptions

### SocialHandlers

- Comments
- Ratings
- Playlists
- Captions

### LivestreamHandlers

- RTMP ingestion
- HLS transcoding
- Waiting rooms
- VOD conversion

### FederationHandlers

- ActivityPub endpoints (WebFinger, NodeInfo, Actor, Inbox, Outbox)
- Federation management
- Redundancy

### ModerationHandlers

- Content moderation
- Abuse reports

### MessagingHandlers

- Direct messages
- Secure messages
- Chat
- Notifications

### PluginHandlers

- Plugin upload/management

### AdminHandlers

- Instance configuration

## Next Steps (Priority Order)

1. **CRITICAL:** Rewrite `routes.go` to use new handler structs
2. **HIGH:** Test compilation and fix errors
3. **HIGH:** Review and fix handler constructors
4. **MEDIUM:** Add missing helper methods to handler structs
5. **MEDIUM:** Update test files to use new package structure
6. **LOW:** Move integration tests to `/tests/integration/`
7. **LOW:** Add repository tests

## Estimated Remaining Time

- Complete Phase 1: 2-3 hours
- Phase 2 (Integration tests): 3-4 hours
- Phase 3 (Repository tests): 5-6 hours
- **Total:** 10-13 hours

## Testing Strategy

Once routes.go is fixed:

```bash
# 1. Test compilation
go build ./internal/httpapi/...

# 2. Run unit tests
go test ./internal/httpapi/... -short

# 3. Check for import cycles
go list -f '{{join .DepsErrors "\n"}}' ./internal/httpapi/...

# 4. Run full test suite
go test ./...
```

## Rollback Instructions

If needed to rollback:

```bash
# This work is in git, so:
git status  # See what's changed
git diff    # Review changes
git reset --hard HEAD  # Nuclear rollback (if needed)

# Or selective rollback:
git checkout HEAD -- internal/httpapi/
```

## Notes

- All file movements done via `git mv` to preserve history
- Package declarations updated systematically
- Shared helpers centralized in `shared/` package
- Integration tests will be moved in Phase 2
- Test fixtures need reorganization in Phase 2

---

**Last Updated:** October 26, 2025
**Next Task:** Update routes.go to wire new handler structs
