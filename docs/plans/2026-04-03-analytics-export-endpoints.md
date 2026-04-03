# Analytics Export Endpoints Implementation Plan

Created: 2026-04-03
Author: yegamble@gmail.com
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

## Summary

**Goal:** Implement server-side analytics export endpoints that generate CSV, JSON, and PDF reports for both video-level and channel-level analytics.

**Architecture:** New `internal/httpapi/handlers/analytics/` package with export handlers. A new `internal/usecase/analytics/export.go` service handles data aggregation and formatting. PDF generation uses `codeberg.org/go-pdf/fpdf` (go-pdf/fpdf fork) with `wcharczuk/go-chart` for retention curve charts. All endpoints require auth and ownership validation.

**Tech Stack:** Go (Chi router), go-pdf/fpdf (PDF), go-chart (charts), encoding/csv (CSV), encoding/json (JSON)

## Scope

### In Scope

- 3 export format endpoints: CSV, JSON, PDF
- Video-level exports (with `videoId` query param)
- Channel-level exports (with optional `channelId` query param)
- All-channels aggregate exports (no `videoId` or `channelId`)
- Authentication and ownership validation
- PDF with: header, stats table, retention chart + table, demographics tables, footer
- Date range filtering (default: last 30 days)
- OpenAPI spec updates
- Postman/Newman collection updates
- Unit tests for all new code

### Out of Scope

- Async/queued export generation (exports are synchronous)
- Export caching / rate limiting (can be added later)
- Email delivery of exports
- Scheduled/recurring exports

## Approach

**Chosen:** New handler package with export service layer

**Why:** Separates export concern from the existing analytics tracking handlers cleanly — the video analytics handler is already focused on event tracking and real-time queries. The export feature has different dependencies (PDF generation, CSV encoding) that don't belong in the tracking code. Cost: one more package, but the boundary is clean.

**Alternatives considered:**
- Extend existing `video/` handlers — rejected because it would bloat the video package with PDF/CSV/chart dependencies unrelated to tracking
- Single handler with format switch — essentially what we're doing, but organized as a package

## Context for Implementer

> Write for an implementer who has never seen the codebase.

- **Patterns to follow:**
  - Handler pattern: `internal/httpapi/handlers/video/video_analytics_handlers.go:171-192` — parse params, call service, respond
  - Route registration: `internal/httpapi/routes.go:832-841` — conditional registration with nil-check on deps
  - Date range parsing: `internal/httpapi/handlers/video/video_analytics_handlers.go:308-331` — `parseDateRange()` helper (reuse this exact pattern)
  - Auth middleware: `middleware.Auth(cfg.JWTSecret)` applied per-route
  - User ID from context: `middleware.GetUserIDFromContext(r.Context())` returns `(uuid.UUID, bool)`
  - Response helpers: `shared.WriteJSON()`, `shared.WriteError()`, `shared.WriteJSONWithMeta()` from `internal/httpapi/shared/response.go`

- **Conventions:**
  - All handlers are methods on a struct with service dependencies injected via constructor
  - Routes registered via `RegisterRoutes(r chi.Router, jwtSecret string)` method
  - Error mapping via `shared.MapDomainErrorToHTTP(err)`
  - Response envelope: `{success, data, error, meta}`
  - Export endpoints return raw content (CSV/PDF) NOT the JSON envelope — they set Content-Type directly

- **Key files:**
  - `internal/domain/analytics.go` — `DailyAnalytics`, `ChannelDailyAnalytics`, `AnalyticsSummary`, `RetentionData` models
  - `internal/port/video_analytics.go` — `VideoAnalyticsRepository` interface
  - `internal/port/video.go` — `VideoRepository.GetByID()` for ownership check
  - `internal/port/channel.go` — `ChannelRepository.CheckOwnership()`, `GetChannelsByAccountID()`
  - `internal/usecase/analytics/service.go` — existing analytics service with `GetVideoAnalyticsSummary()`, `GetDailyAnalyticsRange()`, `GetRetentionCurve()`, `GetChannelDailyAnalyticsRange()`, `GetChannelTotalViews()`
  - `internal/httpapi/handlers/video/video_analytics_interface.go` — `VideoAnalyticsService` interface (the export handler will define its own interface, reusing the same methods)
  - `internal/httpapi/shared/dependencies.go` — `HandlerDependencies` struct where deps are wired

- **Gotchas:**
  - Video ownership: `domain.Video.UserID` is a `string`, while `middleware.GetUserIDFromContext()` returns `uuid.UUID` — must convert for comparison
  - Channel ownership: `port.ChannelRepository.CheckOwnership(ctx, channelID, userID)` returns `(bool, error)`
  - **ChannelRepo type in HandlerDependencies:** `deps.ChannelRepo` is typed as `*repository.ChannelRepository` (concrete type), NOT `port.ChannelRepository` (interface). The ExportService constructor should accept `port.ChannelRepository` (interface) — wiring works because `*repository.ChannelRepository` implements the interface.
  - **GetRetentionCurve takes a single date, not a range:** `analytics.Service.GetRetentionCurve(ctx, videoID, date)` returns retention for one day. For PDF export, use `endDate` from the date range to get the most recent retention snapshot. Do NOT iterate per-day — that would be too slow for large ranges and retention is typically viewed as a point-in-time snapshot.
  - The existing analytics service already has all data-fetching methods needed — the export service wraps them for formatting
  - PDF/CSV responses do NOT use the `shared.WriteJSON` envelope — they write raw bytes with appropriate Content-Type
  - `AnalyticsSummary` already contains `TopCountries`, `DeviceBreakdown`, `TrafficSources`, and `RetentionCurve` fields

- **Domain context:**
  - `DailyAnalytics` has per-day metrics: views, unique_viewers, watch_time, likes, comments, shares + JSON breakdown fields (countries, devices, browsers, traffic_sources)
  - `ChannelDailyAnalytics` has per-day channel metrics: views, unique_viewers, watch_time, subscribers_gained/lost, likes, comments
  - `RetentionData` has `TimestampSeconds` and `ViewerCount` per data point
  - `AnalyticsSummary` aggregates across a date range for a single video

## Assumptions

- The `analytics.Service` already handles all data queries needed — no new repository methods required. Supported by: `internal/usecase/analytics/service.go` methods match all data needs. Tasks 1-4 depend on this.
- The `go-pdf/fpdf` library from Codeberg (`codeberg.org/go-pdf/fpdf`) provides `AddPage()`, `Cell()`, `SetFont()`, `Image()` compatible API. Task 3 depends on this.
- `wcharczuk/go-chart` can render to PNG in-memory via `chart.Render(chart.PNG, &buf)`. Task 3 depends on this.
- Video ownership can be checked via `videoRepo.GetByID()` then comparing `video.UserID` with the authenticated user. Supported by: `domain.Video.UserID` field. Tasks 1-4 depend on this.
- Channel ownership via `channelRepo.CheckOwnership()` and all-channels via `channelRepo.GetChannelsByAccountID()`. Supported by: `internal/port/channel.go:19-21`. Tasks 1-4 depend on this.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| PDF generation slow for large date ranges | Medium | Medium | Set 365-day max date range; stream PDF bytes directly to response writer |
| go-pdf/fpdf API differences from gofpdf | Low | Medium | Library is documented fork; test PDF output in unit tests |
| Chart image quality in PDF | Low | Low | Generate chart at 640x320px; include data table as fallback |
| Large CSV for channels with many videos | Low | Medium | Aggregate at daily level (not per-event); max 365 days |

## Goal Verification

### Truths

1. `GET /api/v1/analytics/export/csv?videoId={id}` returns a valid CSV file with daily time-series data and correct Content-Type/Content-Disposition headers
2. `GET /api/v1/analytics/export/json?videoId={id}` returns the full analytics summary as downloadable JSON with Content-Disposition
3. `GET /api/v1/analytics/export/pdf?videoId={id}` returns a valid PDF with stats table, retention chart, and demographics
4. Channel-level exports work with `channelId` param and without any ID params (all channels)
5. All endpoints return 401 without auth, 403 for non-owner
6. Date params default to last 30 days when omitted
7. OpenAPI spec in `api/openapi_analytics.yaml` documents all 3 export endpoints

### Artifacts

- `internal/httpapi/handlers/analytics/export_handlers.go` — handler implementation
- `internal/httpapi/handlers/analytics/export_handlers_test.go` — unit tests
- `internal/usecase/analytics/export.go` — export service (CSV, JSON, PDF generation)
- `internal/usecase/analytics/export_test.go` — export service tests
- `api/openapi_analytics.yaml` — updated with export endpoints
- `postman/vidra-analytics.postman_collection.json` — updated with export requests

## Progress Tracking

- [x] Task 1: Export service layer (CSV + JSON)
- [x] Task 2: Export HTTP handlers + routing
- [x] Task 3: PDF generation service
- [x] Task 4: PDF export handler integration
- [x] Task 5: OpenAPI + Postman updates
- [x] Task 6: Unit tests for all export code

**Total Tasks:** 6 | **Completed:** 6 | **Remaining:** 0

## Implementation Tasks

### Task 1: Export Service Layer (CSV + JSON)

**Objective:** Create the export service that fetches analytics data and formats it into CSV and JSON.

**Dependencies:** None

**Files:**

- Create: `internal/usecase/analytics/export.go`
- Create: `internal/usecase/analytics/export_test.go`

**Key Decisions / Notes:**

- Define an `ExportService` struct that wraps the existing `port.VideoAnalyticsRepository`, `port.VideoRepository`, and `port.ChannelRepository`
- CSV format: columns `date,views,unique_viewers,watch_time_seconds,likes,comments,shares` — one row per day
- JSON export: return the same `AnalyticsSummary` shape as `GetVideoAnalyticsSummary()` plus daily breakdown
- Channel-level: aggregate `ChannelDailyAnalytics` across the date range
- All-channels: call `channelRepo.GetChannelsByAccountID()` then aggregate across all channels
- Ownership validation methods: `ValidateVideoOwnership(ctx, videoID, userID)` and `ValidateChannelOwnership(ctx, channelID, userID)` — return `domain.ErrForbidden` on mismatch
- CSV generation: use `encoding/csv` writer to `bytes.Buffer`
- JSON export: marshal analytics data to formatted JSON bytes

**Definition of Done:**

- [ ] `ExportService.GenerateCSV(ctx, params)` returns CSV bytes for video/channel/all-channels
- [ ] `ExportService.GenerateJSON(ctx, params)` returns JSON bytes
- [ ] `ExportService.ValidateVideoOwnership()` returns nil or `domain.ErrForbidden`
- [ ] `ExportService.ValidateChannelOwnership()` returns nil or `domain.ErrForbidden`
- [ ] Unit tests cover video CSV, channel CSV, all-channels CSV, JSON export, ownership validation
- [ ] All tests pass

**Verify:**

- `go test -v ./internal/usecase/analytics/... -run TestExport -count=1`

---

### Task 2: Export HTTP Handlers + Routing

**Objective:** Create the HTTP handler package and register export routes.

**Dependencies:** Task 1

**Files:**

- Create: `internal/httpapi/handlers/analytics/export_handlers.go`
- Modify: `internal/httpapi/shared/dependencies.go` (add ExportService field)
- Modify: `internal/app/app.go` (instantiate ExportService and assign to deps.ExportService)
- Modify: `internal/httpapi/routes.go` (register export routes)

**Key Decisions / Notes:**

- Handler struct: `ExportHandler` with `exportService` dependency
- Single `RegisterRoutes(r chi.Router, jwtSecret string)` method
- Routes under `/api/v1/analytics/export/`:
  - `GET /csv` — CSV export
  - `GET /json` — JSON export
  - `GET /pdf` — PDF export (wired in Task 4)
- All routes use `middleware.Auth(cfg.JWTSecret)`
- Parse query params: `videoId`, `channelId`, `start_date`, `end_date`
- Logic: if `videoId` present → video export; if `channelId` present → channel export; neither → all-channels export
- CSV response: `Content-Type: text/csv`, `Content-Disposition: attachment; filename="analytics-{id}.csv"`
- JSON response: `Content-Type: application/json`, `Content-Disposition: attachment; filename="analytics-{id}.json"`
- Error responses use `shared.WriteError()` with standard envelope
- Follow date range parsing pattern from `video_analytics_handlers.go:308-331`
- Register in `routes.go` inside `registerExternalFeatureRoutes()` with nil-check on export service
- **Wiring in app.go:** Instantiate `analytics.NewExportService(deps.AnalyticsRepo, videoRepo, deps.ChannelRepo)` and assign to `deps.ExportService`. Follow the existing pattern at `app.go:543` where `AnalyticsRepo` is created. The ExportService wraps the existing analytics repo (no new DB queries).

**Definition of Done:**

- [ ] `GET /api/v1/analytics/export/csv?videoId=...` returns CSV with correct headers
- [ ] `GET /api/v1/analytics/export/json?videoId=...` returns JSON download
- [ ] Channel-level and all-channels exports work
- [ ] 401 returned without auth token
- [ ] 403 returned for non-owner
- [ ] 400 returned for invalid date format or invalid UUID
- [ ] Routes registered in `routes.go`
- [ ] No diagnostics errors

**Verify:**

- `go build ./...`
- `go test -v ./internal/httpapi/handlers/analytics/... -count=1`

---

### Task 3: PDF Generation Service

**Objective:** Add PDF generation capability using go-pdf/fpdf and go-chart for the retention curve.

**Dependencies:** Task 1

**Files:**

- Create: `internal/usecase/analytics/pdf_export.go`
- Create: `internal/usecase/analytics/pdf_export_test.go`
- Modify: `go.mod` (add `codeberg.org/go-pdf/fpdf` and `github.com/wcharczuk/go-chart/v2`). **Verify first:** run `go get codeberg.org/go-pdf/fpdf` — if it fails to resolve via proxy.golang.org, fall back to `github.com/go-pdf/fpdf`.

**Key Decisions / Notes:**

- Add method `ExportService.GeneratePDF(ctx, params)` returning `([]byte, error)`
- PDF layout:
  1. **Header:** Title (video title or "Channel Analytics" or "All Channels Analytics"), date range, generation timestamp
  2. **Stats Summary Table:** Views, Unique Viewers, Watch Time (formatted as hours), Likes, Comments, Shares, Subscribers (channel only)
  3. **Retention Curve:** Line chart as embedded PNG (640x320px) generated via `go-chart`, rendered to bytes buffer, embedded via `fpdf.RegisterImageOptionsReader()`. **Uses `endDate` from the date range** (most recent retention snapshot) — GetRetentionCurve takes a single date, not a range.
  4. **Retention Data Table:** Timestamp (seconds) | Viewer Count — below the chart
  5. **Demographics — Geography:** Top 10 countries table
  6. **Demographics — Devices:** Device type breakdown table
  7. **Demographics — Traffic Sources:** Top 10 referrer sources table
  8. **Footer:** "Generated by Vidra Core on {timestamp}"
- For channel-level PDFs, retention curve is omitted (retention is per-video only)
- Chart generation: `chart.Chart{Series: []chart.Series{chart.ContinuousSeries{XValues, YValues}}}` → `chart.Render(chart.PNG, &buf)`
- Use `fpdf.New("P", "mm", "A4", "")` for portrait A4
- Font: built-in Helvetica (no external font files needed)

**Definition of Done:**

- [ ] `GeneratePDF()` returns valid PDF bytes for video analytics
- [ ] PDF contains header, stats table, retention chart + table, demographics, footer
- [ ] Channel-level PDF works (no retention chart)
- [ ] All-channels PDF works
- [ ] Chart renders correctly with retention data
- [ ] Empty data handled gracefully (no panic, shows "No data available")
- [ ] Unit tests verify PDF generation succeeds and output is non-empty
- [ ] `go mod tidy` clean

**Verify:**

- `go test -v ./internal/usecase/analytics/... -run TestPDF -count=1`
- `go build ./...`

---

### Task 4: PDF Export Handler Integration

**Objective:** Wire the PDF export into the HTTP handler and complete the `/pdf` route.

**Dependencies:** Task 2, Task 3

**Files:**

- Modify: `internal/httpapi/handlers/analytics/export_handlers.go` (add PDF handler method)

**Key Decisions / Notes:**

- Add `ExportPDF` handler method to `ExportHandler`
- Response: `Content-Type: application/pdf`, `Content-Disposition: attachment; filename="analytics-{id}.pdf"`
- Write PDF bytes directly to `http.ResponseWriter`
- The PDF route was registered in Task 2 but pointed to a placeholder — now connect it to the real handler
- Actually: register the PDF route alongside CSV/JSON in Task 2, calling `h.ExportPDF` directly

**Definition of Done:**

- [ ] `GET /api/v1/analytics/export/pdf?videoId=...` returns PDF with correct Content-Type
- [ ] `GET /api/v1/analytics/export/pdf?channelId=...` returns channel PDF
- [ ] `GET /api/v1/analytics/export/pdf` returns all-channels PDF
- [ ] Auth and ownership checks work for PDF endpoint
- [ ] No diagnostics errors

**Verify:**

- `go build ./...`
- `go test -v ./internal/httpapi/handlers/analytics/... -count=1`

---

### Task 5: OpenAPI + Postman Updates

**Objective:** Document the new export endpoints in OpenAPI and add Postman collection tests.

**Dependencies:** Task 4

**Files:**

- Modify: `api/openapi_analytics.yaml` (add 3 export endpoint paths)
- Modify: `postman/vidra-analytics.postman_collection.json` (add export requests)

**Key Decisions / Notes:**

- OpenAPI: Add paths for `/api/v1/analytics/export/csv`, `/api/v1/analytics/export/json`, `/api/v1/analytics/export/pdf`
- Each path documents: query params (videoId, channelId, start_date, end_date), auth requirement, response content types
- CSV response: `text/csv` with string schema
- JSON response: `application/json` with analytics schema reference
- PDF response: `application/pdf` with binary string schema
- Postman: Add 6 requests (CSV/JSON/PDF for video-level, one channel-level example) with tests for status 200, content-type header

**Definition of Done:**

- [ ] OpenAPI spec has all 3 export paths with correct parameters and responses
- [ ] `make verify-openapi` passes
- [ ] Postman collection has export requests with basic test scripts
- [ ] No validation errors

**Verify:**

- `make verify-openapi`

---

### Task 6: Unit Tests for All Export Code

**Objective:** Ensure comprehensive test coverage for all export functionality.

**Dependencies:** Task 4

**Files:**

- Create or update: `internal/httpapi/handlers/analytics/export_handlers_test.go`
- Update: `internal/usecase/analytics/export_test.go` (if not fully covered in Task 1/3)

**Key Decisions / Notes:**

- Handler tests: use `httptest.NewRecorder()` and mock service
- Test cases:
  - Video CSV export — valid request, correct Content-Type, CSV parseable
  - Video JSON export — valid request, correct Content-Type, JSON parseable
  - Video PDF export — valid request, correct Content-Type, non-empty body
  - Channel CSV/JSON/PDF — with channelId param
  - All-channels — no videoId or channelId
  - Auth failure — no token → 401
  - Ownership failure — non-owner → 403
  - Invalid video ID — bad UUID → 400
  - Invalid date format → 400
  - Date range defaults — no dates → last 30 days applied
- Mock the export service interface for handler tests
- Service tests mock the repository interfaces
- Follow table-driven test pattern from `video_analytics_handlers_unit_test.go`

**Definition of Done:**

- [ ] All handler test cases pass
- [ ] All service test cases pass
- [ ] Coverage ≥ 80% for new packages
- [ ] `make validate-all` passes
- [ ] No diagnostics errors

**Verify:**

- `go test -v ./internal/httpapi/handlers/analytics/... -count=1`
- `go test -v ./internal/usecase/analytics/... -count=1`
- `make validate-all`

## Open Questions

None — all design decisions resolved.

## Deferred Ideas

- Rate limiting on export endpoints (prevent abuse of PDF generation)
- Async export with download link for large date ranges
- Email delivery of scheduled reports
- Export in XLSX format
