# Logging Parity with PeerTube Implementation Plan

Created: 2026-04-06
Author: yegamble@gmail.com
Status: VERIFIED
Approved: Yes
Iterations: 1
Worktree: No
Type: Feature

## Summary

**Goal:** Achieve full logging parity with PeerTube's logging system — file logging with rotation, IP anonymization, domain-specific logger tags, comprehensive audit logging across 8 entity types, enhanced log query APIs, and consistent slog usage across the entire codebase.

**Architecture:** Extend the existing `internal/obs` package with file output, rotation (via `lumberjack`), tags, and audit logging. Migrate all 199 stdlib `log.Print*` and 76 `logrus` calls to `slog`. Enhance admin log APIs to read from log files with date/level/tag filtering, matching PeerTube's `GET /api/v1/server/logs` and `GET /api/v1/server/audit-logs` contracts.

**Tech Stack:** Go `log/slog` (stdlib), `lumberjack` (rotation), existing Chi middleware

## Scope

### In Scope

- File logging with JSON output + rotation (configurable max_file_size, max_files)
- Dual output: stderr (text/JSON based on env) + file (always JSON)
- Logger tags system (domain-specific categorization like PeerTube's `loggerTagsFactory`)
- IP anonymization in HTTP logs (configurable + DNT header support)
- Configurable HTTP request logging (`log_http_requests`, `log_ping_requests`)
- Audit log service with dedicated file (`vidra-audit.log`)
- Audit entity views for 8 PeerTube entity types (videos, users, channels, comments, config, abuses, video imports, channel syncs)
- Enhanced `GET /api/v1/server/logs` with startDate, endDate, level, tagsOneOf filtering
- Enhanced `GET /api/v1/server/audit-logs` with startDate, endDate filtering
- Client log toggle (`accept_client_log` config)
- Migration of all stdlib `log.Print*` (199 calls) and `logrus` (76 calls) to `slog`
- OpenTelemetry trace context (traceId/spanId) in structured log entries
- Config fields for all new logging options

### Out of Scope

- SQL prettification (`prettify_sql`) — PeerTube-specific for Sequelize; not applicable to Go/SQLX
- Bunyan logger adapter — PeerTube-specific for Node.js HTTP signature library
- Log aggregation (ELK, Loki) — infrastructure concern, not application code
- Log shipping/forwarding — handled by container orchestration

## Approach

**Chosen:** Extend existing `internal/obs` package with multi-writer support and new features

**Why:** The `obs` package already has a solid `slog`-based foundation with JSON/text modes, level filtering, context propagation, and security redaction. Building on it avoids introducing a new logging subsystem and keeps all observability concerns in one package.

**Alternatives considered:**
- **Replace with zerolog/zap** — Higher performance but unnecessary; slog is stdlib, already in use, and fast enough for PeerTube parity. Would require rewriting existing obs package and all callsites.
- **Separate logging package** — Would fragment observability concerns across `obs` and a new `logging` package. No benefit over extending `obs`.

## Context for Implementer

> Write for an implementer who has never seen the codebase.

- **Patterns to follow:**
  - Logger construction: `internal/obs/logger.go` — `NewLogger(env, level, writer)` returns `*slog.Logger`
  - HTTP middleware: `internal/middleware/observability.go:30-102` — `LoggingMiddleware` wraps handler, logs method/path/status/duration
  - Config loading: `internal/config/config_load.go:103-104` — `GetEnvOrDefault("LOG_LEVEL", "info")`
  - Context propagation: `obs.ContextWithRequestID(ctx, id)` then `obs.LoggerFromContext(ctx, logger)`
  - Admin handler pattern: `internal/httpapi/handlers/admin/log_handlers.go` — role check, repo call, response

- **Conventions:**
  - All config via env vars with `GetEnvOrDefault`
  - JSON field names: `snake_case`
  - Error wrapping: `fmt.Errorf("operation: %w", err)`
  - Constructor DI: pass dependencies via `New*` constructors
  - Table-driven tests with testify

- **Key files:**
  - `internal/obs/logger.go` — core logger factory (extend this)
  - `internal/middleware/observability.go` — HTTP logging middleware (modify this)
  - `internal/config/config.go:90-91` — LogLevel, LogFormat fields
  - `internal/config/config_load.go:103-104` — env var loading
  - `internal/httpapi/handlers/admin/log_handlers.go` — log API handlers (rewrite these)
  - `internal/httpapi/shared/dependencies.go:127` — `LogRepo any` (wire this properly)
  - `internal/httpapi/routes.go:475-479` — log route registration
  - `internal/app/app.go` — application wiring (45 stdlib log calls to migrate)

- **Gotchas:**
  - `LogRepo` in `dependencies.go` is typed `any` and is NEVER assigned in `app.go` — the log endpoint routes are effectively dead code due to the `if logRepo, ok := deps.LogRepo.(admin.LogRepository); ok {` guard
  - `app.go` line 553 creates a logrus logger: `logger.SetLevel(logrus.InfoLevel)` — this is for the torrent/livestream subsystems
  - The response writer wrapper in observability.go does NOT implement `http.Hijacker` or `http.Flusher` — must be added in Task 3 since WebSocket (chat, livestream) and SSE endpoints require these interfaces
  - **CRITICAL:** `cmd/server/main.go` uses Chi's built-in `middleware.Logger`, NOT the custom `LoggingMiddleware` from `internal/middleware/observability.go`. Task 3 must replace Chi's Logger with the custom one in main.go, or all IP anonymization and configurable logging changes will have zero effect.
  - `cmd/server/main.go` has 6 `log.Fatalf` and 3 `log.Println` calls — must be migrated in Task 7 as the logger bootstrap site

- **Domain context:**
  - PeerTube's audit logger records who did what to which entity, with diff on updates
  - PeerTube's log file format: one JSON object per line (JSONL), read in reverse for recency
  - PeerTube's tags are string arrays attached to log entries for filtering (e.g., `["http"]`, `["ap", "video"]`)
  - `MAX_LOGS_OUTPUT_CHARACTERS` in PeerTube limits API response size to prevent huge payloads

## Assumptions

- `lumberjack` v2 is available as dependency for log rotation — no existing dependency, will need `go get` — Tasks 2, 4 depend on this
- The existing `LogRepository` interface in `log_handlers.go` can be replaced with file-based reading — no database tables exist for logs — Task 6 depends on this
- Handler functions that need audit logging already have access to user identity via `middleware.UserIDKey` context value — Tasks 5 depends on this
- `slog.Handler` interface supports multi-handler composition (tee to file + stderr) — Task 2 depends on this

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Log migration breaks existing log output in tests | Medium | Medium | Run full test suite after each migration batch; update test assertions that depend on specific log output |
| File rotation creates permission issues in containers | Low | High | Default LOG_DIR to empty (disable file logging); only enable when LOG_DIR is explicitly set |
| Audit logging adds latency to CRUD handlers | Low | Medium | Audit writes are async via buffered channel (1000 entries); background goroutine drains to file writer. Never blocks HTTP response. Drops entries if buffer full (with warning). |
| Large log files cause OOM in log API reads | Medium | High | Implement MAX_LOGS_OUTPUT_CHARACTERS (5MB) limit matching PeerTube; read files in reverse line-by-line |

## Goal Verification

### Truths

1. Running with `LOG_DIR=/tmp/vidra-logs` produces both `vidra.log` and `vidra-audit.log` in that directory with JSON entries
2. Log rotation creates rotated files when `vidra.log` exceeds `LOG_ROTATION_MAX_SIZE`
3. `GET /api/v1/server/logs?startDate=...&level=warn` returns only warn+ entries from the log file within the date range
4. `GET /api/v1/server/audit-logs?startDate=...` returns audit entries with user, domain, action, and entity details
5. Creating/updating/deleting a video produces audit log entries with before/after diff on update
6. No `log.Print*` or `logrus` calls remain in production code under `internal/` and `cmd/`
7. HTTP request logs respect `LOG_HTTP_REQUESTS=false` and `LOG_PING_REQUESTS=false` toggles

### Artifacts

1. `internal/obs/logger.go` — enhanced with file writer, multi-handler, tags
2. `internal/obs/audit.go` — new audit logger service (async channel-based)
3. `internal/obs/audit_views.go` — entity audit view types for 8 PeerTube entities
4. `internal/obs/ip_anonymize.go` — IP anonymization utility
5. `internal/obs/otel_handler.go` — OTel trace context injection handler
6. `internal/config/config.go` — extended with all log config fields
7. `internal/middleware/observability.go` — enhanced with IP anonymization, configurable toggles, Hijacker/Flusher
8. `internal/httpapi/handlers/admin/log_handlers.go` — rewritten with file-based reading and filtering
9. `cmd/server/main.go` — logger bootstrap, custom middleware wiring
10. `api/openapi_server_debug.yaml` — updated with query param schemas

## Progress Tracking

- [x] Task 1: Extended Log Configuration
- [x] Task 2: Enhanced Logger — File Output, Rotation, Tags, Multi-Handler
- [x] Task 3: IP Anonymization + Configurable HTTP Logging
- [x] Task 4: Audit Log Service + Entity Views
- [x] Task 5: Wire Audit Logging into Handlers
- [x] Task 6: Enhanced Log APIs (Server Logs, Audit Logs, Client Log Toggle)
- [x] Task 7: Migrate stdlib log → slog
- [x] Task 8: Migrate logrus → slog
- [x] Task 9: OTel Trace Context in Log Entries

**Total Tasks:** 9 | **Completed:** 9 | **Remaining:** 0

## Implementation Tasks

### Task 1: Extended Log Configuration

**Objective:** Add all PeerTube-equivalent logging configuration fields to Vidra Core's config system.
**Dependencies:** None

**Files:**

- Modify: `internal/config/config.go`
- Modify: `internal/config/config_load.go`
- Modify: `.env.example`
- Test: `internal/config/config_load_test.go`

**Key Decisions / Notes:**

- Follow existing pattern at `config.go:90-91` for field declarations
- Follow existing pattern at `config_load.go:103-104` for env var loading
- PeerTube config mapping:
  - `log.level` → `LOG_LEVEL` (already exists)
  - `log.rotation.enabled` → `LOG_ROTATION_ENABLED` (default: true)
  - `log.rotation.max_file_size` → `LOG_ROTATION_MAX_SIZE_MB` (default: 12, int in megabytes — matches lumberjack's native unit)
  - `log.rotation.max_files` → `LOG_ROTATION_MAX_FILES` (default: 20)
  - New: `LOG_ROTATION_MAX_AGE_DAYS` → max days to retain rotated files (default: 0 = disabled)
  - `log.anonymize_ip` → `LOG_ANONYMIZE_IP` (default: false)
  - `log.log_ping_requests` → `LOG_PING_REQUESTS` (default: true)
  - `log.log_http_requests` → `LOG_HTTP_REQUESTS` (default: true)
  - `log.accept_client_log` → `LOG_ACCEPT_CLIENT_LOG` (default: true)
  - New: `LOG_DIR` → directory for log files (default: empty = no file logging)
  - New: `LOG_FILENAME` → log filename (default: "vidra.log")
  - New: `AUDIT_LOG_FILENAME` → audit log filename (default: "vidra-audit.log")

**Definition of Done:**

- [ ] All config fields declared in Config struct
- [ ] All env vars loaded with defaults matching PeerTube
- [ ] `.env.example` updated with documented log config section
- [ ] Config load tests verify defaults and overrides

**Verify:**

- `go test ./internal/config/... -count=1 -run TestLog`

---

### Task 2: Enhanced Logger — File Output, Rotation, Tags, Multi-Handler

**Objective:** Extend `obs.NewLogger` to support dual output (file + stderr), log rotation via lumberjack, and a logger tags system for domain-specific categorization.
**Dependencies:** Task 1

**Files:**

- Modify: `internal/obs/logger.go`
- Create: `internal/obs/multi_handler.go`
- Create: `internal/obs/tags.go`
- Modify: `internal/obs/logger_test.go`
- Create: `internal/obs/multi_handler_test.go`
- Create: `internal/obs/tags_test.go`

**Key Decisions / Notes:**

- Add `gopkg.in/natefinch/lumberjack.v2` dependency for rotation: `go get gopkg.in/natefinch/lumberjack.v2`
- Create `MultiHandler` implementing `slog.Handler` that fans out to multiple handlers (file JSON + stderr text/JSON)
- New constructor: `NewLoggerWithFile(cfg LoggerConfig) (*slog.Logger, io.Closer)` — returns logger + closer for graceful shutdown
  - `LoggerConfig` struct: `Level, Format string; LogDir, Filename string; Rotation RotationConfig`
  - `Format` comes from `config.LogFormat` ("json" or "text") — controls stderr handler format
  - File handler always uses JSON format regardless of `Format` setting
- Tags system: `LoggerTagsFactory(defaultTags ...string) func(tags ...string) []slog.Attr`
  - Returns a function that creates slog attrs with `tags` key containing merged tag arrays
  - Usage: `lTags := obs.LoggerTagsFactory("ap", "video"); logger.Info("processing", lTags("update")...)`
- `MultiHandler` must implement `Enabled`, `Handle`, `WithAttrs`, `WithGroup` from `slog.Handler`
- In `cmd/server/main.go` bootstrap: call `obs.SetGlobalLogger(logger)` with the new file-enabled logger so any callers of `GetGlobalLogger()` get the correct instance
- The returned `io.Closer` must be deferred in `main.go` to flush buffered log writes during shutdown

**Definition of Done:**

- [ ] `NewLoggerWithFile` creates dual-output logger with rotation
- [ ] `MultiHandler` correctly fans log records to both handlers
- [ ] `LoggerTagsFactory` produces tags arrays matching PeerTube's pattern
- [ ] Rotation config respected (max size, max files)
- [ ] When `logDir` is empty, behaves like existing `NewLogger` (stderr only)
- [ ] All tests pass including new multi-handler and tags tests

**Verify:**

- `go test ./internal/obs/... -count=1 -v`

---

### Task 3: IP Anonymization + Configurable HTTP Logging

**Objective:** Add IP anonymization support and configurable HTTP/ping request logging to the HTTP middleware, matching PeerTube's `anonymize_ip`, `log_http_requests`, and `log_ping_requests` options.
**Dependencies:** Task 1, Task 2

**Files:**

- Create: `internal/obs/ip_anonymize.go`
- Create: `internal/obs/ip_anonymize_test.go`
- Modify: `internal/middleware/observability.go`
- Modify: `internal/middleware/observability_test.go`
- Modify: `internal/middleware/observability_edge_case_test.go`
- Modify: `cmd/server/main.go` (replace Chi's `middleware.Logger` with custom `LoggingMiddleware`)

**Key Decisions / Notes:**

- PeerTube uses `anonymize(req.ip, 16, 16)` — despite the "16" parameter, for IPv4 this zeros the last octet (8 bits): `192.168.1.100` → `192.168.1.0`. For IPv6 it zeros the last 80 bits.
- Go implementation: parse IP, zero appropriate bytes, return string
- Honor `DNT: 1` header: anonymize regardless of config when DNT is set (PeerTube: `CONFIG.LOG.ANONYMIZE_IP === true || req.get('DNT') === '1'`)
- `LoggingMiddleware` signature change: accept a config struct instead of just `logger interface{}`
  - `type LoggingConfig struct { Logger *slog.Logger; AnonymizeIP bool; LogHTTPRequests bool; LogPingRequests bool }`
- Skip logging entirely when `LogHTTPRequests` is false
- Skip logging `/api/v1/ping` and `/health` when `LogPingRequests` is false
- Apply IP anonymization to the `ip` field in log entries
- Log 4xx responses at Warn level (matching PeerTube), 5xx at Error, 2xx/3xx at Info
- Add `response_size` and `request_content_length` fields to HTTP log entries (responseWriter.size is already tracked)
- **CRITICAL:** `responseWriter` must implement `http.Hijacker` and `http.Flusher` interfaces by delegating to the underlying `ResponseWriter` (required for WebSocket upgrades in chat/livestream and SSE streaming)
- **CRITICAL:** In `cmd/server/main.go`, replace `middleware.Logger` (Chi's built-in) with the custom `LoggingMiddleware` from `internal/middleware/observability.go`

**Definition of Done:**

- [ ] `AnonymizeIP("192.168.1.100")` returns `"192.168.1.0"`
- [ ] IPv6 anonymization works correctly
- [ ] DNT header triggers anonymization even when config is false
- [ ] `LOG_HTTP_REQUESTS=false` suppresses all HTTP request log entries
- [ ] `LOG_PING_REQUESTS=false` suppresses `/api/v1/ping` and `/health` log entries
- [ ] 4xx responses logged at Warn level, 5xx at Error, 2xx/3xx at Info
- [ ] HTTP log entries include `response_size` and `request_content_length`
- [ ] `responseWriter` implements `http.Hijacker` and `http.Flusher`
- [ ] `cmd/server/main.go` uses custom `LoggingMiddleware` instead of Chi's `middleware.Logger`
- [ ] Existing middleware tests updated for new config struct (both test files)

**Verify:**

- `go test ./internal/obs/... -count=1 -run TestAnonymize`
- `go test ./internal/middleware/... -count=1 -run TestLogging`

---

### Task 4: Audit Log Service + Entity Views

**Objective:** Create a comprehensive audit logging service that writes CRUD actions on entities to a dedicated audit log file, with entity views for all 8 PeerTube-audited entity types and diff-on-update support.
**Dependencies:** Task 2

**Files:**

- Create: `internal/obs/audit.go`
- Create: `internal/obs/audit_views.go`
- Create: `internal/obs/audit_test.go`
- Create: `internal/obs/audit_views_test.go`

**Key Decisions / Notes:**

- PeerTube audit domains and their Vidra Core equivalents:
  1. `videos` — `domain.Video` (VideoAuditView)
  2. `users` — `domain.User` (UserAuditView)
  3. `channels` — `domain.Channel` (ChannelAuditView)
  4. `comments` — `domain.Comment` (CommentAuditView)
  5. `config` — custom config map (ConfigAuditView)
  6. `abuse` — `domain.AbuseReport` (AbuseAuditView)
  7. `video-imports` — `domain.VideoImport` (VideoImportAuditView)
  8. `channel-syncs` — `domain.ChannelSync` (ChannelSyncAuditView)
- `AuditLogger` struct with `Create(domain, user string, entity EntityAuditView)`, `Update(domain, user string, newEntity, oldEntity EntityAuditView)`, `Delete(domain, user string, entity EntityAuditView)`
- `EntityAuditView` interface: `ToLogKeys() map[string]interface{}`
- Each concrete view filters to a set of allowed keys (like PeerTube's `keysToKeep`)
- Update action computes diff between old and new entity keys, prefixes changed values with `new-`
- Audit entries written as JSONL to `vidra-audit.log` via dedicated lumberjack writer
- `AuditLoggerFactory(domain string) *DomainAuditLogger` — convenience constructor per domain
- Audit writes use an async channel-based approach: `AuditLogger` writes entries to a buffered channel, a background goroutine drains the channel to the lumberjack writer. This prevents latency spikes during file rotation. Channel buffer size: 1000 entries. If buffer is full, log a warning and drop the entry (never block the HTTP handler).
- `AuditLogger` must have a `Close()` method that drains remaining entries and closes the file writer (called during graceful shutdown)

**Definition of Done:**

- [ ] `AuditLogger` writes JSONL entries to configured audit log file
- [ ] Each entry contains: timestamp, user, domain, action, entity keys
- [ ] Update action includes diff (old keys + `new-*` keys for changed values)
- [ ] All 8 entity audit views implemented with appropriate key filtering
- [ ] Views only expose safe keys (no passwords, tokens, secrets)
- [ ] Tests verify create/update/delete entry format and diff computation

**Verify:**

- `go test ./internal/obs/... -count=1 -run TestAudit`

---

### Task 5: Wire Audit Logging into Handlers

**Objective:** Add audit log calls to all CRUD handler functions that correspond to PeerTube's audited operations.
**Dependencies:** Task 2, Task 4 (Task 2 for `NewLoggerWithFile` used in app.go wiring; Task 4 for the AuditLogger type)

**Files:**

- Modify: `internal/httpapi/handlers/video/upload_handlers.go`
- Modify: `internal/httpapi/handlers/video/videos.go`
- Modify: `internal/httpapi/handlers/auth/users.go`
- Modify: `internal/httpapi/handlers/admin/user_handlers.go`
- Modify: `internal/httpapi/handlers/channel/channels.go`
- Modify: `internal/httpapi/handlers/social/comments.go`
- Modify: `internal/httpapi/handlers/admin/config_handler.go`
- Modify: `internal/httpapi/handlers/moderation/moderation.go`
- Modify: `internal/httpapi/handlers/video/import_handlers.go`
- Modify: `internal/httpapi/handlers/channel/sync_handlers.go`
- Modify: `internal/httpapi/shared/dependencies.go`
- Modify: `internal/httpapi/routes.go`
- Modify: `internal/app/app.go`
- Test: existing handler test files (verify audit logger is called)

**Key Decisions / Notes:**

- Add `AuditLogger *obs.AuditLogger` to `dependencies.go` shared deps struct
- Wire `AuditLogger` in `app.go` during app initialization
- For each handler: after successful CRUD, call audit logger with user ID from context
- PeerTube gets user from `res.locals.oauth.token.User.username`; Vidra uses `middleware.UserIDKey` context value
- For update operations: capture old entity before the update call, then log diff with new entity
- Audit logging must not affect the HTTP response — errors are logged but swallowed
- Pattern: `if a.auditLogger != nil { a.auditLogger.Create("videos", userID, obs.NewVideoAuditView(video)) }`

**Definition of Done:**

- [ ] Video upload/update/delete produces audit entries
- [ ] User create/update/delete produces audit entries
- [ ] Channel create/update/delete produces audit entries
- [ ] Comment create/delete produces audit entries
- [ ] Config update produces audit entries with diff
- [ ] Abuse create produces audit entries
- [ ] Video import create produces audit entries
- [ ] Channel sync create/delete produces audit entries
- [ ] Audit logger wired in dependencies and app.go
- [ ] Handler tests verify audit logger interaction (mock audit logger)

**Verify:**

- `go test ./internal/httpapi/handlers/... -count=1 -short`

---

### Task 6: Enhanced Log APIs (Server Logs, Audit Logs, Client Log Toggle)

**Objective:** Rewrite the admin log API handlers to read from log files with date/level/tag filtering (matching PeerTube's implementation), and add client log toggle support.
**Dependencies:** Task 2, Task 4

**Files:**

- Modify: `internal/httpapi/handlers/admin/log_handlers.go`
- Modify: `internal/httpapi/handlers/admin/log_handlers_test.go`
- Modify: `internal/httpapi/routes.go`
- Modify: `internal/httpapi/shared/dependencies.go`
- Modify: `api/openapi_server_debug.yaml` (add query params to log endpoints, 403 response for client log)
- Modify: relevant Postman collection in `postman/` (add test requests for filtered logs, audit logs, client log toggle)
- Modify: `.claude/rules/feature-parity-registry.md` (update logging feature status)

**Key Decisions / Notes:**

- Remove `LogRepository` interface — logs are read from files, not database
- `LogHandlers` now depends on `logDir string` and `config` (for accept_client_log toggle)
- `GetServerLogs` reads from `vidra*.log` files in LOG_DIR:
  - Query params: `startDate` (required), `endDate` (optional, default now), `level` (optional, default info), `tagsOneOf` (optional, string array)
  - Read files sorted by mtime desc, parse JSONL, filter by date+level+tags
  - MAX_LOGS_OUTPUT_CHARACTERS = 5MB limit
  - **Response format:** Keep Vidra's standard `{success, data}` envelope for consistency with all other Vidra endpoints (per peertube-parity.md envelope conventions). PeerTube returns raw array; Vidra wraps in envelope. This is an intentional documented divergence.
  - **File reading strategy:** Open files sorted by mtime desc. For each file: read from end backwards line-by-line using a reverse line scanner. Parse each JSON line, check timestamp against date range and level/tag filters. Accumulate output until MAX_LOGS_OUTPUT_CHARACTERS (5MB) is reached or startDate is exceeded. Return results in chronological order.
- `GetAuditLogs` reads from `vidra-audit*.log` files:
  - Query params: `startDate` (required), `endDate` (optional)
  - Same file reading pattern
- `CreateClientLog` checks `accept_client_log` config toggle, returns 403 when disabled
- Route registration no longer guarded by `LogRepo` type assertion — always register
- Run `make verify-openapi` after OpenAPI spec changes

**Definition of Done:**

- [ ] `GET /api/v1/server/logs?startDate=2026-04-06T00:00:00Z` returns filtered log entries from file
- [ ] `level` param filters to specified level and above (debug < info < warn < error)
- [ ] `tagsOneOf` param filters to entries containing at least one matching tag
- [ ] `GET /api/v1/server/audit-logs?startDate=2026-04-06T00:00:00Z` returns audit entries from file
- [ ] `POST /api/v1/server/logs/client` returns 403 when `accept_client_log` is false
- [ ] Output size limited to 5MB
- [ ] Handler tests cover all query param combinations
- [ ] OpenAPI spec updated with query parameter schemas
- [ ] Postman collection updated with test requests
- [ ] Feature parity registry updated
- [ ] `make verify-openapi` passes

**Verify:**

- `go test ./internal/httpapi/handlers/admin/... -count=1 -run TestLog`
- `make verify-openapi`

---

### Task 7: Migrate stdlib log → slog

**Objective:** Replace all `log.Print*`/`log.Fatal*` calls in production code (both `internal/` and `cmd/`) with structured `slog` calls, passing the application logger via constructors.
**Dependencies:** Task 2

**Files:**

- Modify: `cmd/server/main.go` (6 log.Fatalf + 3 log.Println — logger bootstrap site, must create logger via `NewLoggerWithFile` and call `SetGlobalLogger`)
- Modify: `internal/app/app.go` (45 calls — highest priority)
- Modify: `internal/httpapi/routes.go` (16 calls)
- Modify: `internal/usecase/migration_etl/service.go` (23 calls)
- Modify: `internal/livestream/scheduler.go` (21 calls)
- Modify: `internal/worker/activitypub_delivery.go` (12 calls)
- Modify: `internal/livestream/analytics_collector.go` (12 calls)
- Modify: `internal/backup/scheduler.go` (10 calls)
- Modify: `internal/worker/iota_payment_worker.go` (9 calls)
- Modify: `internal/validation/checksum.go` (8 calls)
- Modify: remaining files with 1-6 calls each (~12 files)
- Test: update any tests that assert on log output

**Key Decisions / Notes:**

- `log.Println("message")` → `logger.Info("message")` (or appropriate level)
- `log.Printf("error: %v", err)` → `logger.Error("operation failed", "error", err)`
- `log.Fatalf(...)` → `logger.Error(...); os.Exit(1)` (only in cmd/main.go; everywhere else use error returns)
- In `app.go`: the App struct should hold `*slog.Logger` and pass it to subsystem constructors
- Files that currently don't accept a logger param need constructor changes — prefer adding `logger *slog.Logger` parameter
- Use appropriate levels: startup messages → Info, warnings → Warn, errors → Error, verbose → Debug
- Add domain tags where natural: `logger.Info("message", "tags", []string{"federation"})` in federation code

**Definition of Done:**

- [ ] Zero `log.Print*` calls in production code under `internal/` and `cmd/`
- [ ] All replaced calls use appropriate slog level
- [ ] Logger passed through constructors, not global
- [ ] Domain tags added to key subsystems (federation, livestream, torrent, worker)
- [ ] `go build ./...` succeeds
- [ ] Full test suite passes

**Verify:**

- `grep -rn "log\.Print\|log\.Fatal\|log\.Panic" internal/ --include="*.go" | grep -v _test.go | wc -l` → 0
- `go test -short ./... -count=1`

---

### Task 8: Migrate logrus → slog

**Objective:** Replace all 76 `logrus` calls in production code with `slog`, and remove the logrus dependency.
**Dependencies:** Task 2

**Files:**

- Modify: `internal/app/app.go` (logrus import + SetLevel)
- Modify: `internal/chat/websocket_server.go`
- Modify: `internal/livestream/hls_transcoder.go`
- Modify: `internal/livestream/rtmp_server.go`
- Modify: `internal/livestream/stream_manager.go`
- Modify: `internal/livestream/vod_converter.go`
- Modify: `internal/torrent/client.go`
- Modify: `internal/torrent/manager.go`
- Modify: `internal/torrent/seeder.go`
- Modify: `internal/torrent/tracker.go`
- Modify: `internal/usecase/migration/s3_migration_service.go`
- Test: update corresponding test files

**Key Decisions / Notes:**

- `logrus.WithField("key", val).Info("msg")` → `logger.Info("msg", "key", val)`
- `logrus.WithFields(logrus.Fields{...}).Error("msg")` → `logger.Error("msg", "k1", v1, "k2", v2)`
- `logrus.Infof("msg %s", val)` → `logger.Info("msg", "value", val)` (structured, not formatted)
- All logrus-using types need `logger *slog.Logger` in their constructors
- `app.go` line 553 `logger.SetLevel(logrus.InfoLevel)` — remove entirely (slog level is set at logger creation)
- After migration: run `go mod tidy` to remove logrus from `go.mod` if no longer needed
- Check `go.mod` for logrus — if only used in these files and tests, it can be removed

**Definition of Done:**

- [ ] Zero `logrus` imports in production code under `internal/`
- [ ] All logrus-using types accept `*slog.Logger` in constructors
- [ ] `logrus` removed from `go.mod` (if no other deps need it)
- [ ] Domain tags added: `["livestream"]`, `["torrent"]`, `["chat"]`
- [ ] `go build ./...` succeeds
- [ ] Full test suite passes

**Verify:**

- `grep -rn "logrus" internal/ --include="*.go" | grep -v _test.go | wc -l` → 0
- `go test -short ./... -count=1`

---

### Task 9: OTel Trace Context in Log Entries

**Objective:** Include OpenTelemetry traceId and spanId in structured log output, matching PeerTube's `defaultMeta` pattern.
**Dependencies:** Task 2

**Files:**

- Modify: `internal/obs/logger.go`
- Create: `internal/obs/otel_handler.go`
- Create: `internal/obs/otel_handler_test.go`

**Key Decisions / Notes:**

- PeerTube injects `traceId`, `spanId`, `traceFlags` into every log entry via winston's `defaultMeta` with getters
- Go equivalent: create an `OTelHandler` that wraps another `slog.Handler` and injects trace context from `context.Context`
- Use `go.opentelemetry.io/otel/trace` (already a dependency) to extract span context
- Handler chain: `OTelHandler` → `MultiHandler` → [FileHandler, StderrHandler]
- Only inject when span context is valid (not zero values)
- Fields: `trace_id`, `span_id`, `trace_flags` (matching Go naming conventions)

**Definition of Done:**

- [ ] Log entries within a traced request contain `trace_id` and `span_id`
- [ ] Log entries without trace context omit these fields (no empty strings)
- [ ] OTelHandler correctly delegates to wrapped handler
- [ ] Tests verify trace context injection with mock spans

**Verify:**

- `go test ./internal/obs/... -count=1 -run TestOTel`

---

## Open Questions

1. Should `log.Fatalf` in `app.go` startup be preserved as `os.Exit(1)` after logging, or converted to returned errors? (Recommendation: return errors where possible, only `os.Exit` in `cmd/server/main.go`)

## Deferred Ideas

- Log streaming via WebSocket (real-time log tailing for admin UI)
- Log search indexing for faster queries on large log files
- Structured error codes in log entries (correlating log entries with domain error codes)
- Log sampling for high-throughput paths (rate-limit debug logs in production)
