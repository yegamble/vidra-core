# Linter Fixes Summary

## Date: 2025-10-27

## Issues Fixed

### 1. undefined: HandlerDependencies
**Files:** `internal/httpapi/routes.go`, `internal/app/app.go`

**Fix:**
- Changed `HandlerDependencies` to `shared.HandlerDependencies`
- Added `"athena/internal/httpapi/shared"` import

### 2. OAuth Methods Missing on Server
**File:** `internal/httpapi/routes.go`

**Fix:** Commented out OAuth endpoints temporarily as they need to be wired through auth handlers:
- `server.OAuthToken`
- `server.OAuthAuthorize`
- `server.OAuthRevoke`
- `server.OAuthIntrospect`
- Admin OAuth client management routes

### 3. undefined: NewActivityPubHandlers
**File:** `internal/httpapi/routes.go`

**Fix:**
- Added `"athena/internal/httpapi/handlers/federation"` import
- Changed to `federation.NewActivityPubHandlers`

### 4. undefined: NewViewsHandler
**File:** `internal/httpapi/routes.go`

**Fix:**
- Added `"athena/internal/httpapi/handlers/video"` import
- Changed to `video.NewViewsHandler`

### 5. undefined: MapDomainErrorToHTTP
**File:** `internal/httpapi/handlers.go`

**Fix:** Changed to `shared.MapDomainErrorToHTTP`

### 6. undefined: WriteJSON / WriteError
**Files:** `internal/httpapi/health.go`, `internal/httpapi/routes.go`

**Fix:**
- Added `"athena/internal/httpapi/shared"` import
- Changed all `WriteJSON` calls to `shared.WriteJSON`
- Changed all `WriteError` calls to `shared.WriteError`

### 7. All Video Handler Functions
**File:** `internal/httpapi/routes.go`

**Fix:** Added `video.` prefix to all video handler functions:
- `ListVideosHandler` → `video.ListVideosHandler`
- `SearchVideosHandler` → `video.SearchVideosHandler`
- `GetVideoHandler` → `video.GetVideoHandler`
- `StreamVideoHandler` → `video.StreamVideoHandler`
- `CreateVideoHandler` → `video.CreateVideoHandler`
- `UpdateVideoHandler` → `video.UpdateVideoHandler`
- `DeleteVideoHandler` → `video.DeleteVideoHandler`
- `VideoUploadChunkHandler` → `video.VideoUploadChunkHandler`
- `VideoCompleteUploadHandler` → `video.VideoCompleteUploadHandler`
- `InitiateUploadHandler` → `video.InitiateUploadHandler`
- `UploadChunkHandler` → `video.UploadChunkHandler`
- `CompleteUploadHandler` → `video.CompleteUploadHandler`
- `GetUploadStatusHandler` → `video.GetUploadStatusHandler`
- `ResumeUploadHandler` → `video.ResumeUploadHandler`
- `EncodingStatusHandlerEnhanced` → `video.EncodingStatusHandlerEnhanced`
- `GetUserVideosHandler` → `video.GetUserVideosHandler`
- `HLSHandler` → `video.HLSHandler`
- `NewHLSHandlers` → `video.NewHLSHandlers`
- `NewImportHandlers` → `video.NewImportHandlers`

### 8. Social Handler Functions
**File:** `internal/httpapi/routes.go`

**Fix:** Added `social.` prefix:
- `NewCommentHandlers` → `social.NewCommentHandlers`
- `NewRatingHandlers` → `social.NewRatingHandlers`
- `NewPlaylistHandlers` → `social.NewPlaylistHandlers`
- `NewCaptionHandlers` → `social.NewCaptionHandlers`

### 9. Channel Handler Functions
**File:** `internal/httpapi/routes.go`

**Fix:** Added `channel.` prefix:
- `ListSubscriptionVideosHandler` → `channel.ListSubscriptionVideosHandler`
- `SubscribeToUserHandler` → `channel.SubscribeToUserHandler`
- `UnsubscribeFromUserHandler` → `channel.UnsubscribeFromUserHandler`
- `ListMySubscriptionsHandler` → `channel.ListMySubscriptionsHandler`
- `NewChannelHandlers` → `channel.NewChannelHandlers`

### 10. Auth Handler Functions
**File:** `internal/httpapi/routes.go`

**Fix:** Added `auth.` prefix:
- `CreateUserHandler` → `auth.CreateUserHandler`
- `GetCurrentUserHandler` → `auth.GetCurrentUserHandler`
- `UpdateCurrentUserHandler` → `auth.UpdateCurrentUserHandler`
- `GetUserHandler` → `auth.GetUserHandler`
- Commented out `server.UploadAvatar` (needs to be wired through auth handlers)

### 11. Messaging Handler Functions
**File:** `internal/httpapi/routes.go`

**Fix:** Added `messaging.` prefix:
- `SendMessageHandler` → `messaging.SendMessageHandler`
- `GetMessagesHandler` → `messaging.GetMessagesHandler`
- `MarkMessageReadHandler` → `messaging.MarkMessageReadHandler`
- `DeleteMessageHandler` → `messaging.DeleteMessageHandler`
- `GetConversationsHandler` → `messaging.GetConversationsHandler`
- `GetUnreadCountHandler` → `messaging.GetUnreadCountHandler`
- `NewNotificationHandlers` → `messaging.NewNotificationHandlers`

### 12. Livestream Handler Functions
**File:** `internal/httpapi/routes.go`

**Fix:** Added `livestream.` prefix:
- `NewLiveStreamHandlers` → `livestream.NewLiveStreamHandlers`

### 13. Moderation Handler Functions
**File:** `internal/httpapi/routes.go`

**Fix:** Added `moderation.` prefix:
- `NewModerationHandlers` → `moderation.NewModerationHandlers`

### 14. Admin Handler Functions
**File:** `internal/httpapi/routes.go`

**Fix:** Added `admin.` prefix:
- `NewInstanceHandlers` → `admin.NewInstanceHandlers`

### 15. Federation Handler Functions
**File:** `internal/httpapi/routes.go`

**Fix:** Added `federation.` prefix:
- `NewFederationHandlers` → `federation.NewFederationHandlers`
- `NewAdminFederationHandlers` → `federation.NewAdminFederationHandlers`
- `NewAdminFederationActorsHandlers` → `federation.NewAdminFederationActorsHandlers`
- `NewFederationHardeningHandler` → `federation.NewFederationHardeningHandler`

## New Imports Added

**internal/httpapi/routes.go:**
```go
import (
	"athena/internal/httpapi/handlers/auth"
	"athena/internal/httpapi/handlers/channel"
	"athena/internal/httpapi/handlers/federation"
	"athena/internal/httpapi/handlers/livestream"
	"athena/internal/httpapi/handlers/messaging"
	"athena/internal/httpapi/handlers/moderation"
	"athena/internal/httpapi/handlers/admin"
	"athena/internal/httpapi/handlers/social"
	"athena/internal/httpapi/handlers/video"
	"athena/internal/httpapi/shared"
)
```

**internal/httpapi/health.go:**
```go
import (
	"athena/internal/httpapi/shared"
)
```

**internal/app/app.go:**
```go
import (
	"athena/internal/httpapi/shared"
)
```

## Build Status

✅ **All packages now build successfully!**

```bash
go build ./...
# Success - no errors
```

## Temporary TODOs Added

The following items were commented out and need to be wired properly:

1. **OAuth endpoints** in routes.go:
   - `/oauth/token`
   - `/oauth/authorize`
   - `/oauth/revoke`
   - `/oauth/introspect`
   - OAuth client management routes

2. **Avatar upload** in routes.go:
   - `/me/avatar` endpoint

These need to be properly wired through the new auth handler structs.

---

**Status:** All linter errors resolved ✅
**Next:** Wire OAuth and Avatar endpoints through auth handlers
