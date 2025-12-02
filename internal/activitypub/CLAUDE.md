# ActivityPub Federation - Claude Guidelines

## Overview

Full ActivityPub implementation for federated video sharing, compatible with Mastodon, PeerTube, Pleroma, and Pixelfed.

## Architecture

```
/internal/activitypub/           → HTTP signatures, key management
/internal/domain/activitypub.go  → Domain models (Actor, Activity, VideoObject)
/internal/repository/activitypub_repository.go → Data persistence
/internal/usecase/activitypub/service.go       → Business logic
/internal/httpapi/activitypub.go               → HTTP handlers
/internal/worker/activitypub_delivery.go       → Background delivery
```

## Database Schema

Tables (migration `041_add_activitypub_support.sql`):

| Table | Purpose |
|-------|---------|
| `ap_actor_keys` | RSA key pairs for local actors |
| `ap_remote_actors` | Cached remote actor profiles |
| `ap_activities` | Local and remote activities |
| `ap_followers` | Follower state machine |
| `ap_delivery_queue` | Outbound delivery with retry |
| `ap_received_activities` | Deduplication |
| `ap_video_reactions` | Federated likes/dislikes |
| `ap_video_shares` | Announces/boosts |

## HTTP Signatures

### Signing Outbound Requests

```go
// All outbound activities MUST be signed
signer := activitypub.NewHTTPSigner(privateKey, keyID)
req, _ := http.NewRequest("POST", inboxURL, body)
signer.Sign(req)
```

### Verifying Inbound Requests

```go
verifier := activitypub.NewHTTPVerifier(publicKeyFetcher)
if err := verifier.Verify(req); err != nil {
    return ErrSignatureInvalid
}
```

## Endpoints

### Discovery
- `GET /.well-known/webfinger?resource={uri}` - Actor lookup
- `GET /.well-known/nodeinfo` - NodeInfo discovery
- `GET /nodeinfo/2.0` - Instance metadata

### Actor
- `GET /users/{username}` - Actor profile (Accept: application/activity+json)
- `GET /users/{username}/outbox` - Public activities
- `GET /users/{username}/followers` - Follower collection
- `GET /users/{username}/following` - Following collection

### Inbox
- `POST /inbox` - Shared inbox (optimized)
- `POST /users/{username}/inbox` - Per-user inbox

## Activity Types

### Supported Activities

| Activity | Direction | Description |
|----------|-----------|-------------|
| Follow | Both | Subscribe to actor |
| Accept/Reject | Outbound | Follow response |
| Create | Both | New content |
| Update | Both | Content edit |
| Delete | Both | Content removal |
| Like | Both | Reaction |
| Announce | Both | Share/boost |
| Undo | Both | Reverse activity |
| View | Outbound | Analytics |

### Activity Handling Pattern

```go
func (s *Service) HandleActivity(ctx context.Context, activity Activity) error {
    // 1. Verify signature (already done in middleware)

    // 2. Check deduplication
    if exists, _ := s.repo.ActivityExists(ctx, activity.ID); exists {
        return nil // Already processed
    }

    // 3. Route by type
    switch activity.Type {
    case "Follow":
        return s.handleFollow(ctx, activity)
    case "Like":
        return s.handleLike(ctx, activity)
    // ...
    }
}
```

## Delivery System

### Queue-Based Delivery

1. Activity created → stored in `ap_activities`
2. Followers queried from `ap_followers`
3. Jobs enqueued to `ap_delivery_queue`
4. Worker processes with exponential backoff

### Retry Strategy

```
Attempt 1: Immediate
Attempt 2: 60 seconds
Attempt 3: 4 minutes
Attempt 4: 16 minutes
Attempt 5: 32 minutes
...
Max: 24 hours between retries
```

## Configuration

```bash
ENABLE_ACTIVITYPUB=true
ACTIVITYPUB_DOMAIN=video.example.com
ACTIVITYPUB_DELIVERY_WORKERS=5
ACTIVITYPUB_DELIVERY_RETRIES=10
ACTIVITYPUB_ACCEPT_FOLLOW_AUTOMATIC=true
PUBLIC_BASE_URL=https://video.example.com
```

## Security Considerations

1. **Always verify HTTP signatures** on inbound activities
2. **Cache public keys** with 24h TTL (prevents key-fetch DoS)
3. **Deduplicate activities** to prevent replay attacks
4. **Rate limit inbox endpoints** like any other API
5. **Validate actor URLs** against SSRF (see security/CLAUDE.md)

## Interoperability Testing

Test federation with:
- **Mastodon**: `docker run -p 3000:3000 tootsuite/mastodon`
- **PeerTube**: Official test instance
- **ActivityPub.rocks**: Validation suite

## Debugging

```sql
-- Check delivery queue status
SELECT status, COUNT(*) FROM ap_delivery_queue GROUP BY status;

-- Failed deliveries
SELECT * FROM ap_delivery_queue WHERE status = 'failed' ORDER BY updated_at DESC;

-- Federation health
SELECT domain, COUNT(*) FROM ap_remote_actors GROUP BY domain;
```

## Testing

```bash
# All ActivityPub tests
go test ./internal/activitypub/...
go test ./internal/httpapi -run TestActivityPub
go test ./internal/repository -run TestActivityPub

# HTTP signature tests specifically
go test ./internal/activitypub/... -run HTTPSig
```
