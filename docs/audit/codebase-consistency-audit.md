# Codebase Consistency & Quality Audit

**Date:** 2026-02-15
**Test Status:** All 3,752 tests passing across 64 packages, 313 test files

---

## Critical: Oversized Files (Hard Limit Violations)

Production code files must be under 300 lines (500 is the hard limit). These files significantly exceed both:

| File | Lines | Severity |
|------|-------|----------|
| `internal/usecase/activitypub/service.go` | 2,063 | **Critical** |
| `internal/generated/types.go` | 1,342 | Low (auto-generated) |
| `internal/httpapi/handlers/video/videos.go` | 1,290 | **Critical** |
| `internal/testutil/database.go` | 1,038 | Low (test infra) |
| `internal/repository/video_repository.go` | 1,004 | **Critical** |
| `internal/usecase/e2ee_service.go` | 903 | **Critical** |
| `internal/usecase/encoding/service.go` | 816 | **Critical** |
| `internal/security/virus_scanner.go` | 815 | **Critical** |
| `internal/repository/redundancy_repository.go` | 815 | **Critical** |
| `internal/repository/plugin_repository.go` | 776 | **Critical** |
| `internal/torrent/tracker.go` | 771 | **High** |
| `internal/httpapi/handlers/plugin/plugin_handlers.go` | 771 | **High** |
| `internal/usecase/federation_service.go` | 769 | **High** |
| `internal/usecase/social/service.go` | 755 | **High** |
| `internal/torrent/client.go` | 741 | **High** |
| `internal/httpapi/handlers/auth/oauth.go` | 740 | **High** |
| `internal/repository/video_analytics_repository.go` | 733 | **High** |
| `internal/torrent/seeder.go` | 729 | **High** |
| `internal/httpapi/handlers/livestream/livestream_handlers.go` | 727 | **High** |
| `internal/config/config.go` | 723 | **High** |
| `internal/chat/websocket_server.go` | 705 | **High** |
| `internal/repository/views_repository.go` | 700 | **High** |
| `internal/httpapi/handlers/auth/avatar.go` | 661 | **High** |
| `internal/usecase/redundancy/service.go` | 648 | **High** |
| `internal/torrent/manager.go` | 623 | **High** |
| `internal/livestream/vod_converter.go` | 620 | **High** |
| `internal/app/app.go` | 614 | **High** |
| `internal/repository/livestream_repository.go` | 612 | **High** |
| `internal/httpapi/handlers/messaging/chat_handlers.go` | 598 | **High** |

**Count:** 29 files over 500 lines, many more between 300-500.

### Recommended Refactoring Priority

1. **`activitypub/service.go` (2,063 lines)** - Split into: `follow_service.go`, `activity_service.go`, `delivery_service.go`, `actor_service.go`
2. **`video/videos.go` (1,290 lines)** - Split into: `list_handlers.go`, `crud_handlers.go`, `upload_handlers.go`, `stream_handlers.go`
3. **`video_repository.go` (1,004 lines)** - Split by domain concern: `video_queries.go`, `video_mutations.go`, `video_search.go`
4. **`e2ee_service.go` (903 lines)** - Split into: `key_management.go`, `encryption_service.go`, `device_service.go`

---

## High: Unfinished Code (TODO/FIXME/Not Implemented)

### Production Code TODOs

| File:Line | Issue | Severity |
|-----------|-------|----------|
| `internal/usecase/analytics/service.go:170` | `TODO: Implement video list retrieval and batch processing` | **High** |
| `internal/httpapi/handlers/messaging/chat_handlers.go:172` | `TODO: Check if user is subscriber or has been granted access` | **High** (access control gap) |
| `internal/httpapi/health.go:42-43` | `TODO: Replace with real queue service` (hardcoded values 5 and 10) | **Medium** |

### Not Implemented Operations

| File:Line | Issue | Severity |
|-----------|-------|----------|
| `internal/storage/ipfs_backend.go:63` | IPFS download returns error "not implemented" | **Medium** |
| `internal/storage/ipfs_backend.go:83` | IPFS delete returns error "not implemented" | **Medium** |
| `internal/storage/ipfs_backend.go:90` | IPFS exists check returns error "not implemented" | **Medium** |
| `internal/httpapi/handlers/federation/activitypub.go:235-236` | Inbox GET returns 501 Not Implemented | **Low** (by design) |

### Skipped Tests for Unimplemented Features

| File | Skipped Tests | Issue |
|------|---------------|-------|
| `internal/worker/iota_payment_worker_test.go:389` | `trackConfirmation method not implemented` | Payment feature incomplete |
| `internal/worker/iota_payment_worker_test.go:521` | `maxRetries mechanism not implemented` | Payment feature incomplete |
| `internal/usecase/activitypub/comment_publisher_test.go:482` | `Parent comment author delivery not yet implemented` | Federation gap |
| `internal/usecase/payments/payment_service_test.go:350` | `Requires proper seed decryption mocking` | Payment testing gap |

---

## Medium: Placeholder E2E Tests

**`tests/e2e/workflows_test.go`** contains 14+ test functions that are ALL placeholders with `t.Skip("E2E test requires full application server - placeholder for implementation")`. These create the false impression of E2E test coverage. They should either be implemented or removed and tracked as issues.

---

## Low: Architecture Observations

### Health Check Hardcoded Values
`internal/httpapi/health.go:42-43` returns hardcoded queue depth values:
```go
func() (int, error) { return 5, nil },  // TODO: Replace with real queue service
func() (int, error) { return 10, nil }, // TODO: Replace with real queue service
```
This means the health endpoint always reports healthy queue state regardless of actual conditions.

### IPFS Backend Stub Pattern
The IPFS backend at `internal/storage/ipfs_backend.go` implements the storage interface but has 3 of its methods returning "not implemented" errors. This is acceptable for upload-only workflows but means the storage interface contract is not fully satisfied.

---

## Summary

| Category | Count | Severity |
|----------|-------|----------|
| Files over 500 lines (hard limit) | 29 | Critical/High |
| Production TODOs (unfinished features) | 3 | High/Medium |
| Not-implemented operations | 4 | Medium |
| Skipped tests for missing features | 4 | Medium |
| Placeholder E2E tests | 14+ | Medium |
| Architecture concerns | 2 | Low |

**Recommended Priority:**
1. Split the top 5 largest files (activitypub/service.go, videos.go, video_repository.go, e2ee_service.go, encoding/service.go)
2. Fix access control TODO in chat_handlers.go (security gap)
3. Implement or remove placeholder E2E tests
4. Replace hardcoded health check queue values
