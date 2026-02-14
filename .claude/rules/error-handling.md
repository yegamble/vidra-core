# Error Handling Patterns

Project-specific error conventions for Athena.

## Sentinel Errors

Use predefined sentinel errors from `internal/domain/errors.go` for common cases.

**Standard errors:**
```go
import "athena/internal/domain"

// Check for specific errors
if errors.Is(err, domain.ErrNotFound) {
    return http.StatusNotFound, "Resource not found"
}

// Common sentinel errors:
domain.ErrNotFound           // Resource not found
domain.ErrUnauthorized       // Unauthorized access
domain.ErrForbidden          // Forbidden
domain.ErrValidation         // Validation error
domain.ErrBadRequest         // Bad request
domain.ErrConflict           // Resource already exists
```

**Domain-specific errors:**

User errors:
```go
domain.ErrUserNotFound
domain.ErrUserAlreadyExists
domain.ErrInvalidCredentials
domain.ErrInvalidToken
domain.ErrTokenExpired
```

Video errors:
```go
domain.ErrVideoNotFound
domain.ErrVideoProcessing
domain.ErrVideoFailed
domain.ErrInvalidFormat
domain.ErrFileTooLarge
```

Storage errors:
```go
domain.ErrIPFSUnavailable
domain.ErrStorageError
domain.ErrProcessingError
```

Message errors:
```go
domain.ErrMessageNotFound
domain.ErrConversationNotFound
domain.ErrCannotMessageSelf
```

## DomainError

For errors needing structured details (code, message, context).

**Basic usage:**
```go
import "athena/internal/domain"

// Simple error
err := domain.NewDomainError("UPLOAD_FAILED", "Failed to upload video")

// With additional details
err := domain.NewDomainErrorWithDetails(
    "UPLOAD_FAILED",
    "Failed to upload video",
    fmt.Sprintf("Session ID: %s, Chunk: %d", sessionID, chunkNum),
)
```

**Structure:**
```go
type DomainError struct {
    Code    string `json:"code"`    // Error code (e.g., "VIDEO_NOT_FOUND")
    Message string `json:"message"` // Human-readable message
    Details string `json:"details"` // Optional context (session ID, etc.)
}
```

**Format:** `CODE: Message (Details)` if Details present, else `CODE: Message`

## Error Wrapping

Always wrap errors with context using `fmt.Errorf` with `%w`.

```go
// GOOD - preserves error chain
result, err := repo.GetVideo(ctx, videoID)
if err != nil {
    return nil, fmt.Errorf("failed to get video %s: %w", videoID, err)
}

// GOOD - multiple layers of context
if err := processVideo(ctx, video); err != nil {
    return fmt.Errorf("processing video %s for user %s: %w", video.ID, userID, err)
}
```

**Why wrap:** Preserves error chain for `errors.Is()` and `errors.As()`, adds contextual information for debugging.

## HTTP Status Mapping

Map domain errors to HTTP status codes in handlers:

```go
func mapErrorToHTTP(err error) int {
    switch {
    case errors.Is(err, domain.ErrNotFound):
        return http.StatusNotFound
    case errors.Is(err, domain.ErrUnauthorized):
        return http.StatusUnauthorized
    case errors.Is(err, domain.ErrForbidden):
        return http.StatusForbidden
    case errors.Is(err, domain.ErrValidation), errors.Is(err, domain.ErrBadRequest):
        return http.StatusBadRequest
    case errors.Is(err, domain.ErrConflict):
        return http.StatusConflict
    default:
        return http.StatusInternalServerError
    }
}
```

## When to Use What

| Situation | Use |
|-----------|-----|
| Standard HTTP error | Sentinel error (`domain.ErrNotFound`) |
| Domain-specific error | Sentinel error (`domain.ErrVideoProcessing`) |
| Error needs structured details | `domain.NewDomainError()` |
| Wrapping external error | `fmt.Errorf("context: %w", err)` |
| Checking error type | `errors.Is(err, domain.ErrFoo)` |
| Type assertion | `domainErr, ok := err.(domain.DomainError)` |

## Examples

**Handler with error mapping:**
```go
func (h *VideoHandler) GetVideo(w http.ResponseWriter, r *http.Request) {
    videoID := chi.URLParam(r, "id")

    video, err := h.service.GetVideo(r.Context(), videoID)
    if err != nil {
        status := mapErrorToHTTP(err)
        http.Error(w, err.Error(), status)
        return
    }

    respondJSON(w, http.StatusOK, video)
}
```

**Service with wrapped errors:**
```go
func (s *VideoService) GetVideo(ctx context.Context, id string) (*domain.Video, error) {
    video, err := s.repo.GetByID(ctx, id)
    if err != nil {
        if errors.Is(err, domain.ErrNotFound) {
            return nil, domain.ErrVideoNotFound
        }
        return nil, fmt.Errorf("getting video from repository: %w", err)
    }

    if video.Status == domain.StatusProcessing {
        return nil, domain.ErrVideoProcessing
    }

    return video, nil
}
```

**Repository with DomainError:**
```go
func (r *VideoRepository) GetByID(ctx context.Context, id string) (*domain.Video, error) {
    var video domain.Video
    err := r.db.GetContext(ctx, &video, "SELECT * FROM videos WHERE id = $1", id)
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            return nil, domain.ErrNotFound
        }
        return nil, fmt.Errorf("querying video: %w", err)
    }
    return &video, nil
}
```

## Quick Reference

| Pattern | Example |
|---------|---------|
| Sentinel error | `return domain.ErrNotFound` |
| DomainError | `domain.NewDomainError("CODE", "message")` |
| Wrap error | `fmt.Errorf("context: %w", err)` |
| Check error | `errors.Is(err, domain.ErrNotFound)` |
| Type assert | `domainErr, ok := err.(domain.DomainError)` |
