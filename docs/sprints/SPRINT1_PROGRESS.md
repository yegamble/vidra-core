> **Historical Document:** This file tracked in-progress work during Sprint 1. For the final summary, see `SPRINT1_COMPLETE.md` or `CHANGELOG.md`.

# Sprint 1 Progress: Video Import System

## Completed Components ✓

### 1. Database Migration ✓

**File:** `migrations/043_create_video_imports_table.sql`

- Created `import_status` enum type
- Created `video_imports` table with all required fields
- Added indexes for performance (user_id, status, created_at, etc.)
- Added constraints for data integrity (valid_completion, valid_dates)
- Added triggers for `updated_at` timestamp
- Added comprehensive comments for documentation

### 2. Domain Models ✓

**File:** `internal/domain/import.go`

**Structures:**

- `ImportStatus` enum with 6 states
- `VideoImport` struct with full field mapping
- `ImportMetadata` struct for yt-dlp metadata

**Methods:**

- `Validate()` - Input validation
- `CanTransition()` - State machine validation
- `Start()`, `MarkProcessing()`, `Complete()`, `Fail()`, `Cancel()` - State transitions
- `UpdateProgress()` - Progress tracking
- `SetMetadata()`, `GetMetadata()` - Metadata management
- `GetSourcePlatform()` - Platform detection

**Errors:**

- 10 domain-specific errors (ErrImportNotFound, ErrImportQuotaExceeded, etc.)

### 3. Repository Layer ✓

**File:** `internal/repository/import_repository.go`

**CRUD Operations:**

- `Create()` - Create new import
- `GetByID()` - Retrieve by ID
- `GetByUserID()` - List user imports (paginated)
- `GetPending()` - Get imports for background worker
- `Update()` - Full update
- `Delete()` - Delete import

**Specialized Methods:**

- `CountByUserID()` - Total imports per user
- `CountByUserIDAndStatus()` - Status-specific count
- `CountByUserIDToday()` - Daily quota checking
- `UpdateProgress()` - Atomic progress update
- `UpdateStatus()` - Status-only update
- `UpdateMetadata()` - Metadata-only update
- `MarkFailed()`, `MarkCompleted()` - Terminal state transitions
- `CleanupOldImports()` - Cleanup job for old records
- `GetStuckImports()` - Find stuck/hanging imports

### 4. yt-dlp Wrapper ✓

**File:** `internal/importer/ytdlp.go`

**Core Functionality:**

- `ValidateURL()` - Dry-run validation
- `ExtractMetadata()` - Extract video info without downloading
- `Download()` - Download with progress callback
- `DownloadThumbnail()` - Thumbnail extraction
- `CheckAvailability()` - Installation verification

**Features:**

- Real-time progress parsing (percentage, bytes)
- Support for all yt-dlp formats (YouTube, Vimeo, etc.)
- Context-based cancellation
- Comprehensive error handling
- MP4 format preference with fallback

## Remaining Components

### 5. Import Service (Usecase Layer) - TODO

**File:** `internal/usecase/import/service.go`

**Planned Methods:**

- `ImportVideo()` - Orchestrate import flow
- `CancelImport()` - Cancel with cleanup
- `GetImportStatus()` - Status check
- `ListUserImports()` - Paginated list
- Background worker for queue processing

**Features to Implement:**

- Rate limiting (5 concurrent per user)
- Quota enforcement (100/day per user)
- Integration with encoding service
- File cleanup on failure
- Notification on completion

### 6. API Handlers - TODO

**File:** `internal/httpapi/import_handlers.go`

**Endpoints:**

- `POST /api/v1/videos/imports` - Start import
- `GET /api/v1/videos/imports/:id` - Get status
- `GET /api/v1/videos/imports` - List imports
- `DELETE /api/v1/videos/imports/:id` - Cancel import

### 7. Testing - TODO

**Unit Tests:**

- `internal/repository/import_repository_test.go`
- `internal/importer/ytdlp_test.go`
- `internal/usecase/import/service_test.go`
- `internal/httpapi/import_handlers_test.go`

**Integration Tests:**

- `tests/integration/import_test.go`

**E2E Tests:**

- Full import flow with real yt-dlp

## Next Steps

1. **Create Import Service** (`internal/usecase/import/service.go`)
   - Implement business logic
   - Add rate limiting
   - Add quota checking
   - Integration with video creation and encoding

2. **Create API Handlers** (`internal/httpapi/import_handlers.go`)
   - HTTP endpoints
   - Request validation
   - Response formatting
   - Authentication

3. **Wire Dependencies**
   - Update `internal/app/app.go`
   - Update routes
   - Configuration updates

4. **Write Tests**
   - Unit tests for all components
   - Integration tests for flow
   - Mock yt-dlp for CI/CD

5. **Documentation**
   - API documentation
   - User guide for imports
   - Admin guide for quotas

## Testing Strategy

### Unit Tests (Target: >80% coverage)

- Repository: Mock database with sqlmock
- YtDlp: Mock exec.Command
- Service: Mock repository and yt-dlp
- Handlers: Mock service with httptest

### Integration Tests

- Real PostgreSQL database (Docker)
- Real Redis for rate limiting
- Mock yt-dlp (test binary)
- Test full flow: create → download → encode → complete

### E2E Tests

- Test with actual yt-dlp on test videos
- Test YouTube, Vimeo imports
- Test error scenarios (geo-block, invalid URL)
- Test cancellation at each stage

## Configuration Required

Add to `.env` or environment:

```bash
# Video Import Configuration
ENABLE_VIDEO_IMPORT=true
YTDLP_BINARY_PATH=yt-dlp
IMPORT_OUTPUT_DIR=./storage/imports
IMPORT_MAX_CONCURRENT_PER_USER=5
IMPORT_DAILY_QUOTA_PER_USER=100
IMPORT_MAX_FILE_SIZE_GB=10
IMPORT_CLEANUP_DAYS=30
```

## Database Migration

Run migration:

```bash
atlas migrate apply --dir "file://migrations" \
  --url "postgres://user:pass@localhost:5432/vidra?sslmode=disable"
```

Or with make:

```bash
make migrate
```

## Dependencies

Add to `go.mod` (no new dependencies required):

- Uses standard library `os/exec` for yt-dlp
- Uses existing `jmoiron/sqlx` for database
- Uses existing Chi for routing

External dependency (system-level):

```bash
# Install yt-dlp
pip install yt-dlp
# or
brew install yt-dlp
# or
curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /usr/local/bin/yt-dlp
chmod +x /usr/local/bin/yt-dlp
```

## Progress Summary

| Component | Status | Lines of Code | Tests |
|-----------|--------|---------------|-------|
| Migration | ✓ Complete | 60 | N/A |
| Domain Models | ✓ Complete | 338 | Pending |
| Repository | ✓ Complete | 369 | Pending |
| YtDlp Wrapper | ✓ Complete | 376 | Pending |
| Service | ⏳ In Progress | 0 | Pending |
| API Handlers | ⏳ Pending | 0 | Pending |
| Tests | ⏳ Pending | 0 | 0 |

**Total Lines Written:** 1,143
**Estimated Completion:** 40% of Sprint 1

## Timeline Estimate

- ✓ Day 1-2: Database & Domain (Complete)
- ✓ Day 3-4: Repository (Complete)
- ✓ Day 5-6: YtDlp Wrapper (Complete)
- ⏳ Day 7-8: Import Service (Next)
- ⏳ Day 9-10: API Handlers (Next)
- ⏳ Day 11-12: Testing (Remaining)
- ⏳ Day 13-14: Integration & Documentation (Remaining)

## Known Issues / Considerations

1. **yt-dlp Availability**: Must be installed on server
2. **Disk Space**: Downloads require temporary storage
3. **Network**: Long-running downloads need timeout handling
4. **Cleanup**: Failed imports need automatic cleanup
5. **Rate Limiting**: Need Redis for distributed rate limiting
6. **Geo-restrictions**: Some videos may not be downloadable
7. **Format Selection**: May need user option for quality selection

## Security Considerations

1. **URL Validation**: Strict validation to prevent SSRF
2. **File Path Sanitization**: Prevent directory traversal
3. **Quota Enforcement**: Prevent abuse
4. **Authentication**: Only authenticated users can import
5. **Privacy**: Respect target privacy settings
6. **Storage Isolation**: User files isolated
7. **Command Injection**: Using validated arguments with exec.CommandContext

## Performance Considerations

1. **Concurrent Downloads**: Limit per user (5) and global
2. **Progress Updates**: Batch updates to reduce DB load
3. **Metadata Caching**: Cache extracted metadata
4. **Cleanup Jobs**: Run off-peak hours
5. **Database Indexes**: Added for all query patterns
6. **Connection Pooling**: Use existing pool configuration
