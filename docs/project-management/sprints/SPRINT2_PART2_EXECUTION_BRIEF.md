# Sprint 2 Part 2 - Implementation Execution Brief

**Status:** APPROVED (Conditional)
**Start Date:** 2025-11-17 (after fixes)
**Estimated Completion:** 2025-11-19 (17-24 hours)
**Execution Mode:** PARALLEL with coordination gates

---

## Critical Pre-Flight Checklist (BLOCKING - 30-60 min)

**Owner:** infra-solutions-engineer
**MUST COMPLETE before any implementation begins**

```bash
# 1. Fix AppError compilation issue (20 min)
# Choose one approach:
# Option A: Define AppError in domain/errors.go
# Option B: Use existing error pattern from codebase

# Verify fix:
go test ./internal/domain/... -run=^$

# 2. Resolve observability dependencies (10 min)
go mod tidy
go test ./internal/obs/... -run=^$

# 3. Create IOTA migration skeleton (30 min)
# Create: migrations/042_add_iota_payments.sql
atlas migrate lint --env dev

# 4. Verify all tests compile
go test ./... -run=^$ # Should show "FAIL" but NOT compilation errors
golangci-lint run ./... # Should PASS

# 5. Green light confirmation
# Reply with: "✅ PRE-FLIGHT COMPLETE - All tests compile, linters pass"
```

**Gate:** NO implementation starts until all 5 checks pass.

---

## Epic 1: IOTA Payments (~6-8 hours)

**Agent:** decentralized-systems-security-expert
**Priority:** P0 (Critical Path)
**Dependencies:** Pre-flight complete
**Tests:** 100+ tests must pass

### Implementation Order

```
1. Database Migration (1h)
   File: migrations/042_add_iota_payments.sql
   Tables:
   - iota_wallets (user_id FK, encrypted_seed, seed_nonce, address, balance)
   - iota_payment_intents (user_id FK, video_id FK nullable, amount, address, status, expires_at)
   - iota_transactions (wallet_id FK, tx_hash unique, amount, type, status, confirmations)

   Constraints:
   - Unique index on wallets(user_id)
   - Unique index on transactions(transaction_hash)
   - Check constraint: amount_iota > 0

   Test: atlas migrate apply --env dev
   Gate: Migration applies cleanly, down migration works

2. IOTA Client (2h)
   File: internal/payments/iota_client.go
   Methods:
   - GenerateSeed() ([]byte, error)
   - DeriveAddress(seed []byte, index uint32) (string, error)
   - BuildTransaction(from, to string, amount int64) (*Transaction, error)
   - SubmitTransaction(ctx context.Context, tx *Transaction) (string, error)
   - GetTransactionStatus(ctx context.Context, hash string) (*TxStatus, error)
   - GetBalance(ctx context.Context, address string) (int64, error)

   Library: github.com/iotaledger/iota.go/v3
   Test: go test ./internal/payments -run TestIOTA -v
   Gate: All 25+ client tests passing

3. Payment Service (2h)
   File: internal/usecase/payments/payment_service.go
   Methods:
   - CreateWallet(ctx, userID) (*Wallet, error)
   - GetWallet(ctx, userID) (*Wallet, error)
   - CreatePaymentIntent(ctx, userID, amount, videoID?) (*Intent, error)
   - DetectPayment(ctx, intentID) error
   - ExpireOldIntents(ctx) error
   - GetTransactionHistory(ctx, userID, pagination) ([]Transaction, error)

   Security:
   - AES-256-GCM seed encryption (crypto/aes, crypto/cipher)
   - Generate random nonce (crypto/rand)
   - NEVER log decrypted seeds

   Test: go test ./internal/usecase/payments -v
   Gate: All 20+ service tests passing

4. HTTP Handlers (1h)
   File: internal/httpapi/handlers/payments/payment_handlers.go
   Routes:
   - POST   /api/v1/payments/wallet
   - GET    /api/v1/payments/wallet
   - POST   /api/v1/payments/intent
   - GET    /api/v1/payments/intent/:id
   - GET    /api/v1/payments/transactions

   Auth: All endpoints require JWT authentication
   Validation: Use validator.v10 for request bodies

   Test: go test ./internal/httpapi/handlers/payments -v
   Gate: All 20+ handler tests passing

5. Background Worker (1h)
   File: internal/worker/iota_payment_worker.go
   Tasks:
   - Poll pending payment intents (every 30s)
   - Check address balances
   - Confirm payments when detected
   - Expire old intents (every 5m)

   Pattern: Ticker-based with context cancellation
   Test: go test ./internal/worker -run TestIOTAPayment -v
   Gate: All 15+ worker tests passing

6. Integration & Wiring (1h)
   - Wire dependencies in cmd/server/main.go
   - Add routes to Chi router
   - Start background worker
   - Manual test: create wallet → create intent → simulate payment

   Gate: End-to-end manual test successful
```

### Acceptance Criteria
- ✅ All 100+ tests passing (`go test ./internal/repository ./internal/payments ./internal/usecase/payments ./internal/httpapi/handlers/payments ./internal/worker -run TestIOTA`)
- ✅ Migration applies and rolls back cleanly
- ✅ Seeds encrypted with AES-256-GCM (verify in DB: encrypted_seed is binary)
- ✅ No seeds in logs (grep application logs for "seed")
- ✅ API endpoints return correct status codes
- ✅ Background worker runs without errors
- ✅ Payment detection works (manual verification)

### Integration Points
- **Video Upload:** Link payment intent to video_id during upload
- **Observability:** Emit metrics (iota_payment_intents_total, iota_confirmations_duration_seconds)
- **Notifications:** Trigger notification on payment confirmed

---

## Epic 2: Video Federation (~4-6 hours)

**Agent:** federation-protocol-auditor
**Priority:** P0 (Critical Path)
**Dependencies:** Pre-flight complete (no dependency on Epic 1)
**Tests:** 69+ tests must pass

### Implementation Order

```
1. BuildVideoObject Method (1.5h)
   File: internal/usecase/activitypub/video_publisher.go
   Method: BuildVideoObject(ctx, video *domain.Video) (*VideoObject, error)

   Logic:
   - Fetch user (video owner) for attributedTo
   - Convert duration to ISO 8601 (330s → "PT5M30S")
   - Build HLS URLs with quality variants
   - Map privacy to audience (to/cc fields)
   - Add PeerTube-specific fields (uuid, support, commentsEnabled)
   - Generate hashtags from tags

   Test: go test ./internal/usecase/activitypub -run TestBuildVideoObject -v
   Gate: All 10+ VideoObject tests passing

2. BuildNoteObject Method (1h)
   File: internal/usecase/activitypub/comment_publisher.go
   Method: BuildNoteObject(ctx, comment *domain.Comment) (*NoteObject, error)

   Logic:
   - Fetch comment author
   - Handle threading (inReplyTo for nested comments)
   - Map comment visibility to audience
   - Convert markdown to sanitized HTML

   Test: go test ./internal/usecase/activitypub -run TestBuildNoteObject -v
   Gate: All 8+ Comment tests passing

3. Publishing Methods (1h)
   File: internal/usecase/activitypub/service.go
   Methods:
   - PublishVideo(ctx, video *domain.Video) error
   - PublishComment(ctx, comment *domain.Comment) error

   Logic:
   - Build ActivityPub object
   - Create Create activity
   - Fetch followers
   - Enqueue delivery jobs

   Test: go test ./internal/usecase/activitypub -run TestPublish -v
   Gate: Publishing tests passing

4. Lifecycle Integration (0.5h)
   Files:
   - internal/usecase/video/service.go (hook on processing complete)
   - internal/usecase/comment/service.go (hook on comment create)

   Hook points:
   - After video processing completes and status = completed
   - After comment saved to DB

   Test: go test ./internal/usecase/activitypub -run TestFederationIntegration -v
   Gate: All 24+ integration tests passing

5. End-to-End Validation (1h)
   Manual tests:
   - Upload video → process → verify Create activity in ap_activities table
   - Check delivery queue has jobs for followers
   - Verify VideoObject JSON structure matches PeerTube format
   - (Optional) Federate to test Mastodon instance

   Gate: Activity created, delivery enqueued, JSON valid
```

### Acceptance Criteria
- ✅ All 69+ tests passing (`go test ./internal/usecase/activitypub -v`)
- ✅ ISO 8601 duration format correct (verify "PT5M30S" in JSON)
- ✅ Privacy audience mappings correct (public → to:Public, unlisted → cc:Public)
- ✅ PeerTube-specific fields present (uuid, support, commentsEnabled)
- ✅ Hashtags generated from tags (#golang)
- ✅ Comment threading works (inReplyTo present for replies)
- ✅ Activities stored in ap_activities table
- ✅ Delivery jobs enqueued in ap_delivery_queue

### Integration Points
- **Video Processing:** Hook into video service completion
- **Comment Creation:** Hook into comment service create
- **Observability:** Emit metrics (ap_activities_published_total, ap_delivery_queue_depth)
- **Worker:** Existing delivery worker processes queue

---

## Epic 3: Observability (~4-6 hours)

**Agent:** infra-solutions-engineer
**Priority:** P1 (High, not blocking other epics)
**Dependencies:** Pre-flight complete
**Tests:** 81 tests (70 tests + 11 benchmarks) must pass

### Implementation Order

```
1. Structured Logger (1h)
   File: internal/obs/logger.go
   Implementation:
   - Use log/slog from Go 1.21+
   - JSON handler for production (slog.NewJSONHandler)
   - Text handler for development (slog.NewTextHandler)
   - Security redaction for passwords, tokens, seeds

   Config:
   - LOG_LEVEL env var (debug, info, warn, error)
   - LOG_FORMAT env var (json, text)

   Usage:
   logger.Info("user logged in", "user_id", userID, "ip", ip)
   logger.Error("payment failed", "error", err, "intent_id", intentID)

   Test: go test ./internal/obs -run TestLogger -v
   Gate: All 10+ logger tests passing

2. Prometheus Metrics (2h)
   File: internal/obs/metrics.go
   Collectors (30+ metrics):

   HTTP:
   - http_requests_total (counter: method, path, status)
   - http_request_duration_seconds (histogram: method, path)
   - http_request_size_bytes (histogram: method, path)
   - http_response_size_bytes (histogram: method, path)

   Database:
   - db_connections (gauge: state=idle|in_use|open)
   - db_query_duration_seconds (histogram: query)
   - db_query_errors_total (counter: query, error)

   IPFS:
   - ipfs_pin_duration_seconds (histogram: operation)
   - ipfs_gateway_requests_total (counter: gateway, status)
   - ipfs_errors_total (counter: operation, error)
   - ipfs_pinned_size_bytes (gauge)

   IOTA:
   - iota_payment_intents_total (counter: status)
   - iota_confirmation_duration_seconds (histogram)
   - iota_wallets_total (gauge)
   - iota_errors_total (counter: operation)

   Virus Scanner:
   - virus_scan_duration_seconds (histogram)
   - malware_detections_total (counter: type)
   - virus_scan_errors_total (counter)

   Video Processing:
   - video_encoding_duration_seconds (histogram: quality)
   - video_encoding_queue_depth (gauge)
   - video_processing_errors_total (counter: stage)

   Test: go test ./internal/obs -run TestMetrics -v
   Gate: All 24+ metrics tests passing

3. OpenTelemetry Tracing (1.5h)
   File: internal/obs/tracing.go
   Implementation:
   - OTLP HTTP exporter (go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp)
   - Tracer provider with sampler
   - W3C Trace Context propagation

   Config:
   - OTLP_ENDPOINT env var (e.g., http://localhost:4318)
   - TRACE_SAMPLE_RATE env var (0.0-1.0)

   Usage:
   ctx, span := tracer.Start(ctx, "ProcessVideo")
   defer span.End()
   span.SetAttributes(attribute.String("video_id", id))

   Test: go test ./internal/obs -run TestTracing -v
   Gate: All 13+ tracing tests passing

4. Observability Middleware (1h)
   File: internal/middleware/observability.go
   Middleware:
   - LoggingMiddleware: Log request/response
   - MetricsMiddleware: Record HTTP metrics
   - TracingMiddleware: Inject trace context

   Chain:
   router.Use(ObservabilityStack()) // combines all 3

   Test: go test ./internal/middleware -run TestObservability -v
   Gate: All 15+ middleware tests passing

5. Integration & Backend Setup (0.5h)
   - Add observability middleware to Chi router
   - Start Jaeger via Docker Compose (ports 16686 UI, 4318 OTLP)
   - Expose /metrics endpoint for Prometheus
   - Verify traces in Jaeger UI
   - Verify metrics at /metrics

   Run integration tests: go test ./internal/obs -run TestIntegration -v
   Gate: All 8 integration tests passing, <5ms overhead verified
```

### Acceptance Criteria
- ✅ All 81 tests + benchmarks passing (`go test ./internal/obs ./internal/middleware -v`)
- ✅ Structured logging outputs JSON in production
- ✅ Security redaction works (passwords, tokens never logged)
- ✅ All 30+ Prometheus metrics exported at `/metrics`
- ✅ OpenTelemetry traces appear in Jaeger UI
- ✅ W3C Trace Context propagated across services
- ✅ Performance overhead <5ms (verify with benchmarks: `go test -bench=. -benchmem`)
- ✅ Request IDs in all log entries
- ✅ Errors correlated with traces (trace_id in error logs)

### Integration Points
- **HTTP Layer:** Add middleware to all routes
- **IOTA Payments:** Emit metrics on payment events
- **Video Federation:** Emit metrics on federation events
- **Video Processing:** Emit metrics on encoding progress

---

## Epic 4: Integration & Validation (~2-3 hours)

**Owner:** ALL agents (collaborative)
**Priority:** P0 (Final gate)
**Dependencies:** Epics 1, 2, 3 complete

### Cross-Epic Integration Test

```
Test Scenario: Paid Video Upload with Federation and Observability

1. Setup
   - Start Jaeger: docker-compose up -d jaeger
   - Start app: docker-compose up -d app
   - Verify /health returns 200
   - Verify /metrics returns Prometheus metrics

2. IOTA Payment Flow
   - POST /api/v1/payments/wallet (create wallet)
   - Verify metric: iota_wallets_total=1
   - POST /api/v1/payments/intent (amount=1000000, video_id=null)
   - Verify payment intent created
   - Verify metric: iota_payment_intents_total{status="pending"}=1
   - Simulate payment confirmation (background worker)
   - Verify metric: iota_payment_intents_total{status="paid"}=1

3. Video Upload Flow
   - POST /api/v1/videos (with payment_intent_id)
   - Verify video created
   - Wait for processing (FFmpeg)
   - Verify video status=completed
   - Verify metric: video_encoding_duration_seconds recorded

4. Federation Flow
   - Verify Create activity in ap_activities table
   - Verify delivery jobs in ap_delivery_queue
   - Query ap_activities: should have VideoObject with PT duration
   - Verify metric: ap_activities_published_total=1

5. Observability Validation
   - Check logs: should have structured JSON with trace_id
   - Check /metrics: all 30+ metrics should be present
   - Check Jaeger UI: should show trace with spans:
     - CreateWallet
     - CreatePaymentIntent
     - UploadVideo
     - ProcessVideo
     - PublishVideo
   - Verify trace_id consistent across all spans
   - Verify performance: total request latency <500ms

6. Error Handling
   - POST /api/v1/payments/intent (amount=-100)
   - Verify 400 Bad Request
   - Verify error logged with trace_id
   - Verify metric: http_requests_total{status="400"}

7. Comment Federation
   - POST /api/v1/videos/:id/comments (create comment)
   - Verify Create activity for Note
   - Verify inReplyTo if nested comment
   - Verify metric: ap_activities_published_total=2
```

### Performance Validation

```bash
# Run all benchmarks
go test ./... -bench=. -benchmem -run=^$

# Verify requirements:
# - Observability overhead: <5ms per request
# - Metrics collection: <1ms
# - Trace span creation: <100µs
# - Logger allocation: <500 bytes/op

# Load test (optional)
# Use hey or wrk to test /api/v1/videos endpoint
hey -n 1000 -c 10 http://localhost:8080/api/v1/videos
# Verify: p95 latency <200ms, no errors
```

### Documentation Updates

```
1. Update docs/api/ENDPOINTS.md
   - Add IOTA payment endpoints
   - Add request/response examples
   - Add error codes

2. Update .env.example
   - Add IOTA_NODE_URL
   - Add IOTA_ENCRYPTION_KEY
   - Add OTLP_ENDPOINT
   - Add LOG_LEVEL, LOG_FORMAT

3. Update CLAUDE.md
   - Add IOTA payments section
   - Add observability section
   - Update metrics list

4. Create docs/troubleshooting/IOTA.md
   - Common IOTA issues
   - Testnet setup
   - Wallet recovery

5. Create docs/observability/SETUP.md
   - Jaeger setup
   - Prometheus setup
   - Grafana dashboards (optional)
```

### Final Smoke Test

```bash
# 1. All tests pass
go test ./... -v
# Expected: PASS (all 250+ tests)

# 2. Linters pass
golangci-lint run ./...
# Expected: no errors

# 3. Docker build
docker-compose build
# Expected: successful build

# 4. Migration
atlas migrate apply --env dev
# Expected: migrations apply cleanly

# 5. Health checks
curl http://localhost:8080/health
# Expected: {"status":"ok"}

curl http://localhost:8080/ready
# Expected: {"database":"ok","redis":"ok","ipfs":"ok"}

curl http://localhost:8080/metrics
# Expected: Prometheus metrics (30+ metrics)

# 6. Jaeger UI
open http://localhost:16686
# Expected: Jaeger UI loads, traces visible

# 7. Manual API test
# Create wallet → create intent → upload video → verify federation
# Expected: end-to-end flow works
```

---

## Coordination & Communication

### Daily Standup (15 min, 10:00 AM)

**Format:**
- What I completed yesterday
- What I'm working on today
- Any blockers

**Communication Channel:** GitHub Discussions or Slack

### Progress Tracking

**Update after each phase:**
```markdown
Epic: [Epic Name]
Phase: [Phase Number/Name]
Status: ✅ Complete / 🔄 In Progress / ⚠️ Blocked
Tests Passing: [X/Y]
Time Spent: [hours]
Blocker: [if any]
```

### Integration Points Review (Daily, 15:00)

**Checklist:**
- [ ] Are IOTA metrics being emitted correctly?
- [ ] Are federation activities being traced?
- [ ] Are all services logging with trace_id?
- [ ] Are there any dependency conflicts?

---

## Risk Management

### Known Risks & Mitigation

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| IOTA testnet unavailable | Medium | High | Mock IOTA client in CI, use fallback node |
| Performance overhead >5ms | Low | Medium | Benchmarks validate, optimize if needed |
| Cross-epic integration bugs | Medium | High | Integration tests catch early |
| Timeline overrun | Low | Medium | Parallel execution, clear gates |

### Escalation Path

**If blocked:**
1. Try to resolve independently (30 min)
2. Ask for help in standup
3. Escalate to PM if blocking >2 hours

**If timeline slipping:**
1. Identify critical path bottleneck
2. Reallocate resources
3. Consider descoping non-critical features

---

## Definition of Done

### Epic-Level DoD
- ✅ All tests passing for epic
- ✅ Code reviewed (self-review minimum)
- ✅ Linters pass
- ✅ Documentation updated
- ✅ Manual testing successful

### Sprint-Level DoD
- ✅ All 250+ tests passing
- ✅ All 4 epics complete
- ✅ Cross-epic integration test passing
- ✅ Performance benchmarks met
- ✅ Documentation complete
- ✅ Docker build successful
- ✅ Manual smoke test passed

---

## Timeline Summary

```
Day 1 (2025-11-17):
  09:00-10:00   Pre-flight fixes (infra-solutions-engineer)
  10:00-10:15   Standup (all agents)
  10:15-18:00   Parallel implementation (all agents)
              - IOTA: Phases 1-3 (migration, client, service)
              - Federation: Phases 1-2 (VideoObject, NoteObject)
              - Observability: Phases 1-2 (logger, metrics)
  15:00-15:15   Integration points review
  18:00         End of Day 1 checkpoint
              - Expected: ~60% complete

Day 2 (2025-11-18):
  09:00-10:00   Continue implementation
              - IOTA: Phases 4-5 (handlers, worker)
              - Federation: Phases 3-4 (publishing, hooks)
              - Observability: Phases 3-4 (tracing, middleware)
  10:00-10:15   Standup
  10:15-13:00   Complete implementation
              - IOTA: Phase 6 (integration)
              - Federation: Phase 5 (validation)
              - Observability: Phase 5 (integration)
  13:00-14:00   Lunch break
  14:00-17:00   Cross-epic integration testing (all agents)
  15:00-15:15   Integration points review
  17:00         End of Day 2 checkpoint
              - Expected: Implementation complete, integration in progress

Day 3 (2025-11-19):
  09:00-10:00   Complete integration testing
  10:00-10:15   Final standup
  10:15-12:00   Performance validation & documentation
  12:00-13:00   Final smoke tests
  13:00         Sprint 2 Part 2 COMPLETE
```

**Total:** 17-24 hours across 2.5 days

---

## Success Criteria (Final Gate)

```
✅ All 250+ tests passing
✅ All golangci-lint checks passing
✅ Docker build successful
✅ All migrations apply and rollback cleanly
✅ IOTA payments functional (wallet, intent, detection)
✅ Videos federate to ActivityPub (Create activity sent)
✅ 30+ Prometheus metrics exported at /metrics
✅ OpenTelemetry traces viewable in Jaeger
✅ Performance benchmarks met (<5ms observability overhead)
✅ End-to-end integration test passing
✅ Documentation updated
✅ Manual smoke test successful
```

**If all criteria met:** Sprint 2 Part 2 APPROVED FOR MERGE

---

**Execution Brief Compiled by:** Project Manager Agent
**Date:** 2025-11-17
**Next Action:** Apply pre-flight fixes and BEGIN IMPLEMENTATION
