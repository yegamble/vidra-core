# Federation Documentation

This directory contains documentation for federated protocols and integrations.

## Federation Implementations

### ActivityPub

Full PeerTube-compatible federation with WebFinger, NodeInfo, and HTTP Signatures.

**Features:**
- Follow/Accept/Reject (follower management)
- Create/Update/Delete (content lifecycle)
- Like/Undo (reactions)
- Announce/Undo (shares/boosts)
- View (analytics)

**Endpoints:**
- `/.well-known/webfinger` - Actor discovery
- `/.well-known/nodeinfo` - Instance metadata
- `/users/{username}` - Actor profiles
- `/inbox` - Shared inbox
- `/users/{username}/inbox` - Per-user inbox

### ATProto (Bluesky) - BETA

Optional Bluesky integration for cross-platform content syndication.

**Status:** 75% complete, BETA
- PDS configuration
- BlueSky account linking
- Content syndication
- Known limitations documented

## Testing Coverage

- **[ACTIVITYPUB_TEST_COVERAGE.md](ACTIVITYPUB_TEST_COVERAGE.md)** - ActivityPub test coverage report

## Configuration

Enable federation via environment variables:

```bash
ENABLE_ACTIVITYPUB=true
ACTIVITYPUB_DOMAIN=video.example.com
ACTIVITYPUB_DELIVERY_WORKERS=5
ACTIVITYPUB_DELIVERY_RETRIES=10
ACTIVITYPUB_ACCEPT_FOLLOW_AUTOMATIC=true
PUBLIC_BASE_URL=https://video.example.com
```

## Quick Links

- [Main README](../../README.md)
- [Architecture Documentation](../architecture/)
- [API Federation Spec](../../api/openapi_federation.yaml)
- [Deployment Guide](../deployment/)

## Interoperability

Compatible with:
- **Mastodon** - Full bidirectional federation
- **PeerTube** - Video federation, comments, follows
- **Pleroma** - Activity interchange
- **Pixelfed** - Media federation
- **Any ActivityPub platform** following W3C recommendation

## Federation Flow

**Outbound (Publishing):**
1. Local activity triggers activity creation
2. Activity stored with `local=true`
3. Followers fetched from database
4. Delivery jobs enqueued
5. Background worker processes queue
6. Activities signed with HTTP Signatures

**Inbound (Receiving):**
1. Activity arrives at inbox
2. HTTP Signature verified
3. Activity deduplicated
4. Activity routed to handler
5. State changes persisted
6. Responses sent (follows)

## Performance Considerations

- Use shared inbox when available
- Scale delivery workers based on volume
- Remote actors cached for 24h
- All foreign keys indexed
