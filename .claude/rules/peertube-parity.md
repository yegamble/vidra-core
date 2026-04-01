# PeerTube API Parity Guardrails

Rules for maintaining PeerTube API compatibility in Vidra Core. Vidra targets full API compatibility with PeerTube while extending with Vidra-specific features.

## Response Envelope

**All Vidra API endpoints MUST use the shared response helpers** from `internal/httpapi/shared/response.go`:

| Function | When to Use |
|----------|------------|
| `shared.WriteJSON(w, status, data)` | Single-object responses |
| `shared.WriteJSONWithMeta(w, status, data, meta)` | List responses with pagination |
| `shared.WriteError(w, status, err)` | Error responses |

**Never use** `json.NewEncoder(w).Encode()` in Vidra API handlers. This bypasses the envelope.

**Exceptions:** Federation handlers (`internal/httpapi/handlers/federation/`) and OAuth handlers (`internal/httpapi/handlers/auth/oauth.go`) return protocol-specific JSON formats (JSON-LD, JRD+JSON, OAuth2 tokens) and correctly bypass the envelope.

### Envelope Shape

```json
// Success:  { "success": true,  "data": {...},  "meta": {"total": N, "limit": M, "offset": O} }
// Error:    { "success": false, "error": {"code": "...", "message": "...", "details": "..."} }
```

PeerTube uses `{ total, data }` for lists. Vidra maps `total` to `meta.total`.

## List Endpoint Pagination

All list endpoints MUST:

1. Accept `start` (offset) and `count` (limit) query parameters (PeerTube convention)
2. Return `Meta` with `Total`, `Limit`, `Offset` via `WriteJSONWithMeta()`
3. Default limit: 15 items. Maximum limit: 100.
4. Return empty array `[]` (not `null`) when no results

```go
shared.WriteJSONWithMeta(w, http.StatusOK, videos, &shared.Meta{
    Total:  totalCount,
    Limit:  limit,
    Offset: offset,
})
```

## Error Status Mapping

Use `shared.MapDomainErrorToHTTP(err)` for consistent HTTP status codes. Domain errors defined in `internal/domain/errors.go` map to standard HTTP statuses per `internal/httpapi/shared/response.go`.

Never hardcode HTTP status for domain errors — let the mapper handle it.

## Route Conventions

### PeerTube-Compatible Routes (MUST preserve)

These routes are used by PeerTube clients and federation peers:

- `/api/v1/videos/*` — video CRUD, search, categories
- `/api/v1/users/*` — user management, auth
- `/api/v1/video-channels/*` — channel management
- `/api/v1/videos/upload` — legacy upload
- `/api/v1/videos/upload-resumable` — resumable upload alias
- `/api/v1/search/videos` — video search
- `/api/v1/config` — instance configuration
- `/api/v1/admin/*` — admin endpoints
- `/feeds/podcast/videos.xml` — podcast RSS

### Vidra Extensions (additional routes)

Vidra-specific routes do not conflict with PeerTube routes:

- `/api/v1/uploads/*` — chunked upload (Vidra-native)
- `/api/v1/payments/*` — IOTA payments
- `/api/v1/messages/*` — secure messaging

### Route Aliases

When PeerTube uses a different path for the same functionality, register both:

```go
// Vidra-native path
r.Post("/initiate", handler)
// PeerTube-compatible alias
r.Post("/upload-resumable", handler)
```

## JSON Field Naming

- Use `snake_case` for all JSON field names (PeerTube convention)
- Go struct tags: `json:"field_name"`
- Acronyms in JSON stay lowercase: `video_id` not `videoID`
- Boolean fields: positive naming (`is_public` not `not_private`)

## Field Compatibility

When PeerTube returns a field, Vidra must return the equivalent:

| PeerTube Field | Vidra Equivalent | Notes |
|---------------|-----------------|-------|
| `total` (top level) | `meta.total` | Pagination count |
| `data` (array) | `data` (array) | Same field name |
| `video.id` | `data.id` | UUID string |
| `video.uuid` | `data.id` | Vidra uses single ID |
| `video.shortUUID` | Not implemented | Deferred |

## Testing for Parity

When implementing PeerTube-compatible endpoints:

1. Check PeerTube's source at `server/core/controllers/api/` for the expected response shape
2. Check PeerTube's validators at `server/core/middlewares/validators/` for required fields
3. Verify response matches PeerTube's REST API docs: https://docs.joinpeertube.org/api-rest-reference.html
4. Test both the Vidra path and PeerTube alias (if applicable)
