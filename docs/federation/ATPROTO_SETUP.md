# ATProto (Bluesky) Integration Setup Guide

This guide explains how to set up and configure ATProto (Authenticated Transfer Protocol) integration with Bluesky for cross-platform content syndication.

## Status

**Current Implementation**: 75% complete, BETA

- ✅ PDS (Personal Data Server) client implementation
- ✅ BlueSky account linking
- ✅ Basic content syndication (video posts)
- ⚠️ Limited support for comments/replies
- ⚠️ Manual syndication only (no automatic cross-posting yet)
- ❌ Federation discovery incomplete

**Production Readiness**: NOT RECOMMENDED for production use

- Use for testing and experimentation only
- Known limitations and breaking changes expected
- Limited error handling and retry logic
- No performance optimization

---

## Architecture Overview

```
Athena Video Platform
        ↓
   ATProto Client
        ↓
   Personal Data Server (PDS)
        ↓
   Bluesky Network
```

**Components**:

1. **ATProto Client** (`/internal/atproto/client.go`) - HTTP client for AT Protocol
2. **PDS Connector** (`/internal/atproto/pds.go`) - Connects to Personal Data Server
3. **Account Service** (`/internal/usecase/atproto/account_service.go`) - Manages Bluesky accounts
4. **Syndication Service** (`/internal/usecase/atproto/syndication_service.go`) - Syndicates content

---

## Prerequisites

### 1. Bluesky Account

You need a Bluesky account to use ATProto integration.

**Get Invite Code**:

- Request invite at [bsky.app](https://bsky.app)
- Or get invite from existing Bluesky user
- Invite codes are limited during beta

**Create Account**:

1. Go to <https://bsky.app/signup>
2. Enter invite code
3. Choose handle (e.g., `@yourname.bsky.social`)
4. Create password
5. Verify email

### 2. App Password

Generate an app-specific password for API access.

**Steps**:

1. Log into Bluesky
2. Go to Settings → App Passwords
3. Click "Add App Password"
4. Name: "Athena Integration"
5. Copy generated password (shown once!)

**Security Note**: Never use your main account password. Always use app passwords.

### 3. Personal Data Server (PDS)

**Default PDS**: `https://bsky.social`

- Most users use the default Bluesky PDS
- No setup required for default PDS

**Self-Hosted PDS** (Advanced):

- See [ATProto PDS Documentation](https://atproto.com/guides/self-hosting)
- Requires domain name and SSL certificate
- Not recommended for most users

---

## Configuration

### Environment Variables

Add to `.env` file:

```bash
# ATProto / Bluesky Integration
ENABLE_ATPROTO=true                          # Enable ATProto integration
ATPROTO_PDS_URL=https://bsky.social          # Personal Data Server URL
ATPROTO_HANDLE=yourname.bsky.social          # Your Bluesky handle
ATPROTO_APP_PASSWORD=xxxx-xxxx-xxxx-xxxx     # App password (NOT your main password)

# Optional Settings
ATPROTO_SYNC_ENABLED=false                   # Auto-sync new videos (BETA, default: false)
ATPROTO_SYNC_PUBLIC_ONLY=true                # Only sync public videos
ATPROTO_MAX_RETRIES=3                        # Retry failed requests
ATPROTO_TIMEOUT=30                           # Request timeout (seconds)
```

### Configuration Validation

**Test Configuration**:

```bash
# Verify ATProto client
curl -X POST http://localhost:8080/api/v1/admin/atproto/test-connection

# Expected response:
{
  "status": "success",
  "pds_url": "https://bsky.social",
  "handle": "yourname.bsky.social",
  "authenticated": true
}
```

**Check Account Linking**:

```bash
# Get linked Bluesky account
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/atproto/account

# Expected response:
{
  "did": "did:plc:abcd1234...",
  "handle": "yourname.bsky.social",
  "linked_at": "2025-01-17T12:00:00Z",
  "status": "active"
}
```

---

## Usage

### Linking Bluesky Account

**API Endpoint**: `POST /api/v1/atproto/link`

**Request**:

```bash
curl -X POST http://localhost:8080/api/v1/atproto/link \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "handle": "yourname.bsky.social",
    "app_password": "xxxx-xxxx-xxxx-xxxx"
  }'
```

**Response**:

```json
{
  "did": "did:plc:abcd1234efgh5678ijkl9012mnop3456",
  "handle": "yourname.bsky.social",
  "display_name": "Your Name",
  "linked_at": "2025-01-17T12:00:00Z"
}
```

**What Happens**:

1. Authenticates with Bluesky PDS
2. Retrieves DID (Decentralized Identifier)
3. Stores account credentials securely
4. Establishes session for API calls

### Syndicating Video to Bluesky

**Manual Syndication** (Current):

**API Endpoint**: `POST /api/v1/atproto/videos/{videoId}/syndicate`

**Request**:

```bash
curl -X POST http://localhost:8080/api/v1/atproto/videos/{videoId}/syndicate \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "include_thumbnail": true,
    "custom_text": "Check out my new video!"
  }'
```

**Response**:

```json
{
  "uri": "at://did:plc:abcd.../app.bsky.feed.post/3k2...",
  "cid": "bafyreib...",
  "syndicated_at": "2025-01-17T12:05:00Z",
  "bluesky_url": "https://bsky.app/profile/yourname.bsky.social/post/3k2..."
}
```

**What Gets Syndi cated**:

- Video title
- Video description (truncated to 300 chars)
- Thumbnail image (if `include_thumbnail: true`)
- Link back to Athena video
- Tags (as hashtags)

**Automatic Syndication** (BETA, disabled by default):

Enable via config:

```bash
ATPROTO_SYNC_ENABLED=true
ATPROTO_SYNC_PUBLIC_ONLY=true
```

When enabled:

- New public videos automatically posted to Bluesky
- Processing delay: ~30 seconds after video finishes processing
- Only videos with `privacy=public` are synced
- Failed syncs logged (no retry yet)

### Unlinking Bluesky Account

**API Endpoint**: `DELETE /api/v1/atproto/unlink`

**Request**:

```bash
curl -X DELETE http://localhost:8080/api/v1/atproto/unlink \
  -H "Authorization: Bearer $TOKEN"
```

**Response**:

```json
{
  "status": "unlinked",
  "message": "Bluesky account unlinked successfully"
}
```

---

## Technical Details

### ATProto Concepts

**DID (Decentralized Identifier)**:

- Unique identifier for Bluesky account
- Format: `did:plc:abc123...` or `did:web:example.com`
- Portable across PDS instances
- Used for all API operations

**PDS (Personal Data Server)**:

- Hosts user's data and content
- Default: `bsky.social`
- Can be self-hosted
- Handles authentication and authorization

**Lexicons**:

- Schema definitions for AT Protocol records
- Video posts use `app.bsky.feed.post` lexicon
- Images use `app.bsky.embed.images` lexicon

**Records**:

- JSON documents stored in PDS
- Each record has a URI: `at://{did}/{collection}/{rkey}`
- Content-addressed via CID (Content Identifier)

### Authentication Flow

```
1. User provides handle + app password
   ↓
2. Client calls PDS createSession
   POST https://bsky.social/xrpc/com.atproto.server.createSession
   {
     "identifier": "yourname.bsky.social",
     "password": "xxxx-xxxx-xxxx-xxxx"
   }
   ↓
3. PDS returns access + refresh tokens
   {
     "accessJwt": "eyJ...",
     "refreshJwt": "eyJ...",
     "did": "did:plc:...",
     "handle": "yourname.bsky.social"
   }
   ↓
4. Client stores tokens for API calls
   ↓
5. Client uses accessJwt for authenticated requests
   Authorization: Bearer eyJ...
   ↓
6. Refresh tokens when expired (every 2 hours)
   POST https://bsky.social/xrpc/com.atproto.server.refreshSession
```

### Syndication Implementation

**Current Implementation** (as of v0.75):

```go
// Simplified pseudocode
func SyndicateVideo(ctx context.Context, videoID uuid.UUID) error {
    // 1. Fetch video details
    video := videoRepo.Get(ctx, videoID)

    // 2. Prepare post content
    post := &PostRecord{
        Type: "app.bsky.feed.post",
        Text: formatVideoText(video),
        CreatedAt: time.Now(),
        Embed: &EmbedExternal{
            Type: "app.bsky.embed.external",
            External: &External{
                URI: video.PublicURL,
                Title: video.Title,
                Description: truncate(video.Description, 300),
                Thumb: uploadThumbnail(ctx, video.ThumbnailURL),
            },
        },
        Facets: extractHashtags(video.Tags),
    }

    // 3. Create record on PDS
    response := atprotoClient.CreateRecord(ctx, &CreateRecordRequest{
        Collection: "app.bsky.feed.post",
        Record: post,
    })

    // 4. Store syndication record
    syndicationRepo.Create(ctx, &Syndication{
        VideoID: videoID,
        ATProtoURI: response.URI,
        ATProtoCID: response.CID,
        SyndicatedAt: time.Now(),
    })

    return nil
}
```

**Limitations**:

- No support for comments/replies (yet)
- No support for video uploads to Bluesky (only links)
- No automatic updates when video is edited
- No deletion sync (deleting video doesn't delete Bluesky post)

---

## Known Limitations

### Current Limitations (BETA)

1. **No Video Uploads**
   - Cannot upload video files to Bluesky
   - Only external links supported
   - Bluesky videos limited to 1 minute, 50MB

2. **Limited Federation**
   - No automatic discovery of Bluesky users
   - Cannot follow Bluesky users from Athena
   - Cannot import Bluesky content to Athena

3. **No Real-Time Sync**
   - Manual syndication only (or delayed auto-sync)
   - No webhook support
   - Updates not pushed automatically

4. **Comment Limitations**
   - Comments on Bluesky posts not synced back
   - Cannot reply to Bluesky comments from Athena
   - One-way syndication only

5. **Error Handling**
   - Limited retry logic
   - No exponential backoff
   - Failed syncs not queued for retry

6. **Performance**
   - No batching support
   - Each syndication is individual API call
   - No caching of PDS responses

### Planned Improvements (Roadmap)

**Phase 2** (Target: Q2 2025):

- [ ] Automatic video upload to Bluesky (if < 1 min)
- [ ] Comment synchronization (bidirectional)
- [ ] Improved error handling and retries
- [ ] Batch syndication support

**Phase 3** (Target: Q3 2025):

- [ ] Federation discovery
- [ ] Follow Bluesky users from Athena
- [ ] Real-time webhook support
- [ ] Full bidirectional sync

---

## Troubleshooting

### Issue: Authentication Failed

**Symptoms**:

```
Error: authentication failed: invalid credentials
```

**Solutions**:

1. Verify app password (not main password)
2. Regenerate app password in Bluesky settings
3. Check handle is correct (include `.bsky.social`)
4. Verify PDS URL is correct

**Debug**:

```bash
# Test authentication manually
curl -X POST https://bsky.social/xrpc/com.atproto.server.createSession \
  -H "Content-Type: application/json" \
  -d '{
    "identifier": "yourname.bsky.social",
    "password": "xxxx-xxxx-xxxx-xxxx"
  }'
```

### Issue: Syndication Failed

**Symptoms**:

```
Error: failed to create record: invalid post
```

**Solutions**:

1. Check video is public (private videos can't be syndicated)
2. Verify video has title and description
3. Check thumbnail is accessible
4. Ensure text length is within limits

**Debug**:

```bash
# Check video details
curl http://localhost:8080/api/v1/videos/{videoId}

# Check syndication logs
docker logs athena | grep "atproto"
```

### Issue: Token Expired

**Symptoms**:

```
Error: token expired
```

**Solutions**:

1. Tokens refresh automatically every 2 hours
2. If manual refresh needed:

```bash
curl -X POST http://localhost:8080/api/v1/atproto/refresh \
  -H "Authorization: Bearer $TOKEN"
```

### Issue: PDS Unreachable

**Symptoms**:

```
Error: dial tcp: lookup bsky.social: no such host
```

**Solutions**:

1. Check internet connectivity
2. Verify PDS URL in config
3. Check DNS resolution
4. Verify firewall rules

**Debug**:

```bash
# Test PDS connectivity
curl https://bsky.social/xrpc/_health

# Expected: 200 OK
```

---

## Security Considerations

### App Password Storage

**Current Implementation**:

- App passwords encrypted at rest (AES-256)
- Stored in `atproto_accounts` table
- Encryption key from environment variable
- Tokens refreshed automatically

**Best Practices**:

1. Use app passwords, never main password
2. Rotate app passwords regularly (every 90 days)
3. Revoke unused app passwords
4. Monitor syndication logs for unauthorized activity

### Privacy

**What's Shared**:

- Public video metadata (title, description, tags)
- Thumbnail images
- Link to video on Athena instance
- User's Bluesky handle

**What's NOT Shared**:

- Private/unlisted videos
- User's main Bluesky password
- Email addresses
- IP addresses
- View counts (unless explicitly included)

### Rate Limiting

**Bluesky Rate Limits** (as of January 2025):

- 1000 requests/hour per account
- 100 posts/hour per account
- 50 images/hour per account

**Athena Protection**:

- Internal rate limiting (300 req/hour)
- Exponential backoff on errors
- Circuit breaker on repeated failures

---

## API Reference

### Link Account

```http
POST /api/v1/atproto/link
Authorization: Bearer {token}
Content-Type: application/json

{
  "handle": "yourname.bsky.social",
  "app_password": "xxxx-xxxx-xxxx-xxxx",
  "pds_url": "https://bsky.social"  // Optional, defaults to bsky.social
}
```

### Unlink Account

```http
DELETE /api/v1/atproto/unlink
Authorization: Bearer {token}
```

### Syndicate Video

```http
POST /api/v1/atproto/videos/{videoId}/syndicate
Authorization: Bearer {token}
Content-Type: application/json

{
  "include_thumbnail": true,
  "custom_text": "Check out my new video!",  // Optional
  "tags": ["video", "tutorial"]              // Optional, adds hashtags
}
```

### Get Syndication Status

```http
GET /api/v1/atproto/videos/{videoId}/syndication
Authorization: Bearer {token}
```

### Refresh Token

```http
POST /api/v1/atproto/refresh
Authorization: Bearer {token}
```

---

## Development & Testing

### Enable Debug Logging

```bash
LOG_LEVEL=debug
ATPROTO_DEBUG=true
```

### Test with Staging PDS

```bash
ATPROTO_PDS_URL=https://staging.bsky.dev
```

### Mock ATProto Client

For testing without real Bluesky account:

```go
// internal/atproto/mock_client.go
type MockATProtoClient struct {
    mock.Mock
}

func (m *MockATProtoClient) CreateRecord(ctx context.Context, req *CreateRecordRequest) (*CreateRecordResponse, error) {
    args := m.Called(ctx, req)
    return args.Get(0).(*CreateRecordResponse), args.Error(1)
}
```

---

## Related Documentation

- [ActivityPub Federation](README.md) - Full federation guide
- [Federation Test Coverage](ACTIVITYPUB_TEST_COVERAGE.md)
- [API Documentation](../../api/openapi_federation.yaml)
- [ATProto Specification](https://atproto.com/specs/atp)
- [Bluesky Documentation](https://docs.bsky.app)

---

## Support & Feedback

**Report Issues**:

- GitHub Issues: Tag with `atproto` label
- Security Issues: <security@athena.com>

**Feature Requests**:

- Discuss in GitHub Discussions
- Tag with `federation` and `enhancement`

**Community**:

- Discord: #federation channel
- Matrix: #athena-federation:matrix.org
