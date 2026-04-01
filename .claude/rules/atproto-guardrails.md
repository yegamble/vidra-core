# ATProto (BlueSky) Integration Guardrails

Rules for working with AT Protocol federation, DID resolution, and record management in Vidra Core.

## DID Validation

DIDs must match `did:plc:*` or `did:web:*` format. Reject arbitrary strings passed as DIDs.

```go
if !strings.HasPrefix(did, "did:plc:") && !strings.HasPrefix(did, "did:web:") {
    return fmt.Errorf("invalid DID format: %s", did)
}
```

Validate DIDs received from: config (`cfg.ATProtoDID`), PDS session responses, resolved handles, and external AT URIs.

## AT URI Parsing

AT URIs follow `at://{did}/{collection}/{rkey}` format. Always validate parts count before indexing.

```go
// REQUIRED: strip prefix, then bounds check before accessing parts
parts := strings.SplitN(strings.TrimPrefix(atURI, "at://"), "/", 3)
if len(parts) < 3 {
    return fmt.Errorf("malformed AT URI: %s", atURI)
}
repo, collection, rkey := parts[0], parts[1], parts[2]
```

Reference: `internal/usecase/atproto_features.go:164` — follow this exact pattern.

## Record Keys (rkeys)

- Alphanumeric + hyphens only, max 512 characters
- No leading or trailing hyphens
- Typically TID format (timestamp-based identifier) for app.bsky records

## Lexicon NSIDs

Valid XRPC method NSIDs used in this codebase:

| NSID | Purpose |
|------|---------|
| `com.atproto.server.createSession` | Authentication |
| `com.atproto.server.refreshSession` | Token refresh |
| `com.atproto.repo.createRecord` | Post creation |
| `com.atproto.repo.uploadBlob` | Media upload |
| `com.atproto.repo.getRecord` | Record retrieval |
| `com.atproto.identity.resolveHandle` | Handle → DID |
| `app.bsky.feed.getAuthorFeed` | Feed retrieval |

New XRPC calls must use dot-separated reverse-DNS NSIDs. Custom lexicons use the `app.vidra.*` namespace.

## Session Security

- **Never log** access tokens or refresh tokens — not even at debug level.
- Session tokens must be encrypted at rest via `AtprotoSessionStore.SaveSession()` which accepts an encryption key.
- Session refresh uses `com.atproto.server.refreshSession` — handle 401 responses by re-authenticating, not by retrying with the same expired token.
- The `sessMu` channel-based mutex in `atproto_service.go` serializes session operations — respect this lock.

## Best-Effort Pattern

**ATProto operations must NEVER block core video functionality.**

The `AtprotoPublisher` interface (`internal/usecase/atproto_service.go`) is designed as best-effort:
- `PublishVideo` errors are logged but do not fail the video upload
- `PublishComment` errors do not prevent comment creation
- `PublishVideoBatch` collects per-video results independently

When adding new ATProto integrations, follow this pattern: wrap in a goroutine with recovery, log errors, never return ATProto errors to the user.

## XRPC Client Requirements

- Always pass `ctx` from the request — never `context.Background()` in request handlers
- Set HTTP client timeouts (`http.Client.Timeout` or `context.WithTimeout`)
- Use the configured PDS URL (`cfg.ATProtoPDS`) — never hardcode `bsky.social`
- Include `Content-Type: application/json` for procedure calls
- Include `Authorization: Bearer {accessJwt}` for authenticated calls

## Testing

- Mock the ATProto PDS using the Docker mock service on port 19200 (see `docker-mock-services.md`)
- Unit tests must mock HTTP calls — never connect to a real PDS
- Test both `did:plc:` and `did:web:` DID formats
- Test session expiry and refresh flows
