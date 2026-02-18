# HTTP API Module - Claude Guidelines

## Overview

Chi-based HTTP API layer with middleware stack, request validation, and structured error responses.

## Router Architecture

### Middleware Stack (Order Matters)

1. `RequestID` - Generate/propagate X-Request-ID
2. `RealIP` - Extract client IP from proxies
3. `Logger` - Structured request logging
4. `Recoverer` - Panic recovery
5. `Timeout(60s)` - Request timeout
6. `Compress` - Gzip responses
7. `CORS` - Cross-origin handling
8. `Auth` - JWT validation
9. `RateLimit` - Sliding window limiting

### Route Patterns

```go
r.Route("/api/v1", func(r chi.Router) {
    r.Use(middleware.Auth)

    r.Route("/videos", func(r chi.Router) {
        r.Get("/", h.ListVideos)
        r.Post("/", h.CreateVideo)
        r.Route("/{videoID}", func(r chi.Router) {
            r.Get("/", h.GetVideo)
            r.Put("/", h.UpdateVideo)
            r.Delete("/", h.DeleteVideo)
        })
    })
})
```

## Request Handling

### Handler Pattern

```go
func (h *Handler) CreateVideo(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()

    // 1. Parse and validate input
    var req CreateVideoRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        h.respondError(w, r, ErrBadRequest.Wrap(err))
        return
    }

    if err := h.validator.Validate(req); err != nil {
        h.respondError(w, r, ErrValidation.Wrap(err))
        return
    }

    // 2. Call usecase
    video, err := h.videoUC.Create(ctx, req.ToDomain())
    if err != nil {
        h.respondError(w, r, err)
        return
    }

    // 3. Return response
    h.respondJSON(w, r, http.StatusCreated, video.ToResponse())
}
```

### Validation

Use struct tags with centralized validator:

```go
type CreateVideoRequest struct {
    Title       string   `json:"title" validate:"required,min=1,max=200"`
    Description string   `json:"description" validate:"max=5000"`
    Privacy     string   `json:"privacy" validate:"required,oneof=public private unlisted"`
    Tags        []string `json:"tags" validate:"max=30,dive,max=50"`
}
```

## Response Envelope

All responses use the `shared.Response` envelope:

```go
type Response struct {
    Data    interface{} `json:"data,omitempty"`
    Error   *ErrorInfo  `json:"error,omitempty"`
    Success bool        `json:"success"`
    Meta    *Meta       `json:"meta,omitempty"`
}

type ErrorInfo struct {
    Code    string `json:"code"`
    Message string `json:"message"`
    Details string `json:"details,omitempty"`
}
```

### Standard Error Mapping

| Domain Error | HTTP Status | Type |
|--------------|-------------|------|
| `ErrNotFound` | 404 | `/errors/not-found` |
| `ErrUnauthorized` | 401 | `/errors/unauthorized` |
| `ErrForbidden` | 403 | `/errors/forbidden` |
| `ErrValidation` | 400 | `/errors/validation` |
| `ErrConflict` | 409 | `/errors/conflict` |
| `ErrRateLimit` | 429 | `/errors/rate-limit` |

## File Uploads

### Chunked Upload Flow

1. `POST /api/v1/uploads/initiate` - Initialize session, get `sessionId`
2. `POST /api/v1/uploads/{sessionId}/chunks` - Upload 32MB chunks
3. `POST /api/v1/uploads/{sessionId}/complete` - Finalize and process

### Security Requirements

- Validate Content-Type header
- Check file magic bytes (not just extension)
- Enforce size limits
- Virus scan before processing
- Store outside web root

## Idempotency

Support `Idempotency-Key` header for:

- POST requests that create resources
- Upload operations
- Payment operations

Store keys in Redis with 24h TTL.

## Health Endpoints

- `GET /health` - Liveness (always 200 if server running)
- `GET /ready` - Readiness (checks DB, Redis, IPFS, queue depth)

## Testing

### Handler Tests

```go
func TestCreateVideo(t *testing.T) {
    // Setup
    h := NewHandler(mockUsecase)

    tests := []struct {
        name       string
        body       string
        wantStatus int
    }{
        {"valid", `{"title":"Test"}`, http.StatusCreated},
        {"invalid", `{"title":""}`, http.StatusBadRequest},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            req := httptest.NewRequest("POST", "/videos", strings.NewReader(tt.body))
            rec := httptest.NewRecorder()

            h.CreateVideo(rec, req)

            assert.Equal(t, tt.wantStatus, rec.Code)
        })
    }
}
```

## Performance Considerations

- Use `http.TimeoutHandler` for slow operations
- Stream large responses (don't buffer in memory)
- Set appropriate `Content-Length` headers
- Enable gzip for JSON responses >1KB
- Use connection pooling for upstream services

## Observability

Each request automatically gets:

- Request ID (X-Request-ID header)
- Structured log entry with duration
- Prometheus metrics (latency, status codes)
- OpenTelemetry trace span
