# Go Concurrency & Context Guardrails

Rules for goroutine safety, context propagation, and concurrency patterns in Vidra Core.

## Context Propagation

### HTTP Handlers: Always Use `r.Context()`

```go
// CORRECT: request-scoped context with deadlines, cancellation, auth
ctx := r.Context()
result, err := service.DoWork(ctx, input)

// WRONG: loses request timeout, cancellation, and auth info
result, err := service.DoWork(context.Background(), input)
```

**Exception:** Fire-and-forget goroutines that must outlive the request (e.g., `emitFollowActivity` in federation) intentionally use `context.Background()`. Document why when using this pattern.

### Service/Repository Layer: Pass Context Through

Every function that does I/O (DB, HTTP, IPFS, IOTA node) must accept `context.Context` as its first parameter and pass it to the underlying call.

```go
func (s *service) ProcessVideo(ctx context.Context, videoID string) error {
    video, err := s.repo.GetByID(ctx, videoID)  // passes ctx
    // ...
}
```

## Goroutine Safety

### Recovery Is Mandatory

Every `go func()` must recover from panics. An unrecovered panic in a goroutine crashes the entire server.

```go
go func() {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("recovered panic in background task: %v", r)
        }
    }()
    // work here
}()
```

### Goroutine Leak Prevention

Goroutines started in request handlers MUST respect `ctx.Done()`:

```go
go func(ctx context.Context) {
    defer func() { /* recovery */ }()
    select {
    case <-ctx.Done():
        return // request cancelled
    case result := <-workChan:
        // process result
    }
}(ctx)
```

Never start a goroutine in a handler without a cancellation mechanism.

## Synchronization Patterns

### sync.Mutex vs sync.Map

| Use | Pattern |
|-----|---------|
| Complex state with read-modify-write | `sync.Mutex` or `sync.RWMutex` |
| Append-mostly cache, no read-modify-write | `sync.Map` |
| Session serialization with select support | Channel-based mutex (see below) |

### Channel-as-Mutex Pattern

The codebase uses `chan struct{}` for session serialization (e.g., `atproto_service.go` `sessMu`):

```go
type service struct {
    sessMu chan struct{} // capacity 1, acts as mutex
}

// Initialize — channel starts EMPTY (unlocked)
s.sessMu = make(chan struct{}, 1)

// Acquire — send blocks if channel is full (another goroutine holds the lock)
s.sessMu <- struct{}{}
defer func() { <-s.sessMu }() // always release via defer (receive frees the slot)
```

Reference: `internal/usecase/atproto_service.go:106` (init), `:271-272` (acquire/release).

**Tradeoffs:** Supports `select` with timeout (useful for session refresh). But if a goroutine panics while holding the lock, the channel stays full — deadlock. Always use `defer` for release and `recover` in the goroutine.

**Prefer `sync.Mutex`** for simple mutual exclusion without select requirements.

## External Call Timeouts

All external calls MUST have timeouts. Never use `http.Get()`, `http.Post()`, or `http.DefaultClient` — these have no timeout.

```go
// CORRECT: context-aware with timeout
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()
req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
resp, err := client.Do(req)

// WRONG: no timeout, potential goroutine leak
resp, err := http.Get(url)
```

Similarly for database: use `db.QueryContext(ctx, ...)` not `db.Query(...)`.

## Common Pitfalls

### defer in Loops

`defer` runs when the function returns, not when the loop iteration ends:

```go
// WRONG: all files closed at function return, not per-iteration
for _, path := range paths {
    f, _ := os.Open(path)
    defer f.Close() // leak until function returns
}

// CORRECT: wrap in closure
for _, path := range paths {
    func() {
        f, _ := os.Open(path)
        defer f.Close()
        // use f
    }()
}
```

### Range Variable Capture (pre-Go 1.22)

Go 1.22+ fixes this, but be aware of the pattern:

```go
// Pre-Go 1.22: captures loop variable by reference
for _, item := range items {
    go func() {
        process(item) // BUG: all goroutines see the last item
    }()
}

// CORRECT: pass as parameter
for _, item := range items {
    go func(it Item) {
        process(it)
    }(item)
}
```

Vidra Core uses Go 1.24, so this is fixed by the compiler. But the parameter-passing style is still clearer.

### Race Detector

Always run tests with `-race` for packages that use goroutines:

```bash
go test -race ./internal/usecase/...
go test -race ./internal/worker/...
go test -race ./internal/livestream/...
```

All shared mutable state must be protected by a mutex, channel, or atomic operation.
