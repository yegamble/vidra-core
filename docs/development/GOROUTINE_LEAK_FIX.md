# Goroutine Leak Fix - Rate Limiter

## Problem

The rate limiter in `/home/user/athena/internal/middleware/ratelimit.go` had a critical goroutine leak:

- Line 29: `go rl.cleanupVisitors()` runs forever without shutdown mechanism
- No way to stop the cleanup goroutine when rate limiter is no longer needed
- This causes goroutine leaks in long-running applications

## Solution

### 1. Added Shutdown Mechanism to RateLimiter

**File: `/home/user/athena/internal/middleware/ratelimit.go`**

- Added `done` channel for shutdown signaling
- Added `wg` sync.WaitGroup to track cleanup goroutine
- Added `shutdownOnce` to ensure idempotent shutdown
- Added `isShutdown` atomic bool to track state
- Implemented `Shutdown()` and `ShutdownWithContext()` methods
- Made cleanup goroutine respond to shutdown signal

### 2. Created RateLimiterManager

**File: `/home/user/athena/internal/middleware/manager.go`**

- Manages lifecycle of all rate limiters in the application
- Tracks all created rate limiters for centralized shutdown
- Provides `CreateRateLimiter()` method to create and track limiters
- Provides `Shutdown()` method to shutdown all managed limiters

### 3. Updated Application Structure

**File: `/home/user/athena/internal/app/app.go`**

- Added `rateLimiterManager` field to Application struct
- Initialize manager in `New()` function
- Call `rateLimiterManager.Shutdown()` in application shutdown

### 4. Updated Route Registration

**File: `/home/user/athena/internal/httpapi/routes.go`**

- Updated to accept RateLimiterManager parameter
- Create rate limiters through manager instead of directly
- Use `.Limit` method as middleware handler

### 5. Comprehensive Tests

**File: `/home/user/athena/internal/middleware/ratelimit_leak_test.go`**

- Test for goroutine leaks when creating/destroying rate limiters
- Test that cleanup goroutine stops on shutdown
- Test idempotent shutdown behavior
- Test context timeout handling
- Test race conditions with `-race` flag
- Test that rate limiter continues working during graceful shutdown

## Key Features of the Fix

1. **No Goroutine Leaks**: Cleanup goroutines are properly terminated
2. **Graceful Shutdown**: Rate limiting continues during shutdown
3. **Idempotent**: Shutdown can be called multiple times safely
4. **Context Support**: Respects context timeouts during shutdown
5. **Thread-Safe**: All operations are protected by appropriate locks
6. **Centralized Management**: All rate limiters tracked and shutdown together

## Testing

All tests pass including race detection:

```bash
go test -v -race ./internal/middleware -run TestRateLimiter
```

## Migration Notes

When using rate limiters in the application:

1. Always create through `RateLimiterManager.CreateRateLimiter()`
2. Never create rate limiters directly with `NewRateLimiter()` unless managing shutdown manually
3. Ensure application calls `rateLimiterManager.Shutdown()` during graceful shutdown

## Files Modified

1. `/home/user/athena/internal/middleware/ratelimit.go` - Fixed implementation
2. `/home/user/athena/internal/middleware/manager.go` - New manager
3. `/home/user/athena/internal/middleware/ratelimit_leak_test.go` - Tests
4. `/home/user/athena/internal/app/app.go` - Integrated manager
5. `/home/user/athena/internal/httpapi/routes.go` - Use managed limiters
