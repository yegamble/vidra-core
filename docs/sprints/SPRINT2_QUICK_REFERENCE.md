# Sprint 2 Quick Reference

## Sprint Goal

Implement IOTA payments, complete ActivityPub video federation, establish observability, and configure Go-Atlas.

**Duration:** 3-4 days (22-28 hours with 7 agents)

---

## Agent Assignments

| Agent | Epic | Hours |
|-------|------|-------|
| Decentralized Systems Security Expert | IOTA Payments (lead) | 8-12h |
| Federation Protocol Auditor | ActivityPub Video Federation (lead) | 6-8h |
| Infra Solutions Engineer | Observability + Atlas (lead) | 8-11h |
| API Edge Tester | IOTA HTTP API | 2h |
| Golang Test Guardian | All tests | 8h |
| Go Backend Reviewer | Code reviews | 4h |
| Decentralized Video PM | Coordination | 2h |

---

## Epic 1: IOTA Payments (8-12 hours)

**Files to Create:**

1. `/migrations/058_create_iota_payments_tables.sql` - DB schema
2. `/internal/domain/iota.go` - Domain models
3. `/internal/iota/client.go` - IOTA node client
4. `/internal/iota/wallet.go` - Wallet management
5. `/internal/iota/transaction.go` - Transaction building
6. `/internal/crypto/aes.go` - Seed encryption
7. `/internal/repository/iota_repository.go` - Persistence
8. `/internal/usecase/payments/iota_service.go` - Business logic
9. `/internal/worker/iota_payment_worker.go` - Background polling
10. `/internal/httpapi/handlers/payments/iota_handlers.go` - HTTP API
11. Tests: `*_test.go` for all modules

**Endpoints:**

- `POST /api/v1/payments/wallet` - Create wallet
- `GET /api/v1/payments/wallet` - Get wallet balance
- `POST /api/v1/payments/intent` - Create payment intent
- `GET /api/v1/payments/intent/:id` - Get payment status
- `GET /api/v1/payments/transactions` - List transactions

**Dependencies:**

```bash
go get github.com/iotaledger/iota.go/v3
go get github.com/iotaledger/iota.go/v3/nodeclient
```

**Environment Variables:**

```bash
IOTA_NODE_URL=https://api.testnet.shimmer.network
IOTA_ENCRYPTION_KEY=<32-byte-hex-key>
IOTA_PAYMENT_POLL_INTERVAL=30s
ENABLE_IOTA=true
```

---

## Epic 2: ActivityPub Video Federation (6-8 hours)

**Files to Create/Modify:**

1. `/internal/usecase/activitypub/video.go` - VideoObject builder (NEW)
2. `/internal/usecase/activitypub/comment.go` - Comment federation (NEW)
3. `/internal/usecase/activitypub/service.go` - Add PublishVideo method (MODIFY)
4. `/internal/usecase/encoding/service.go` - Trigger PublishVideo on completion (MODIFY)
5. `/internal/usecase/comment/service.go` - Trigger PublishComment (MODIFY)
6. Tests: `*_test.go` for all modules

**Integration Points:**

- Video upload completion → PublishVideo() → Create activity → Deliver to followers
- Comment creation → PublishComment() → Create activity → Deliver to followers

**Testing:**

- Videos should appear on Mastodon/PeerTube after processing
- Comments should federate as Note objects

---

## Epic 3: Observability (6-8 hours)

**Files to Create:**

1. `/internal/obs/logger.go` - Structured logging (slog)
2. `/internal/obs/context.go` - Request-scoped logging
3. `/internal/obs/tracing.go` - OpenTelemetry setup
4. `/internal/middleware/logging.go` - Request logger (MODIFY)
5. `/internal/middleware/tracing.go` - Trace middleware (NEW)
6. `/internal/metrics/metrics.go` - Expanded Prometheus metrics (MODIFY)

**Environment Variables:**

```bash
LOG_LEVEL=info
LOG_FORMAT=json
OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318
OTEL_SERVICE_NAME=athena
```

**New Metrics:**

- HTTP: `athena_http_requests_total`, `athena_http_request_duration_ms`
- Database: `athena_db_connections_open`, `athena_db_queries_total`
- IPFS: `athena_ipfs_pinning_total`, `athena_ipfs_pin_duration_ms`
- IOTA: `athena_iota_payments_confirmed_total`, `athena_iota_payments_pending`
- Virus: `athena_virus_scan_infected_total`

**Dependencies:**

```bash
go get go.opentelemetry.io/otel
go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp
go get go.opentelemetry.io/otel/sdk/trace
go get go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp
```

---

## Epic 4: Go-Atlas Configuration (2-3 hours)

**Files to Create:**

1. `/atlas.hcl` - Atlas configuration
2. `/.github/workflows/atlas-lint.yml` - CI integration
3. `/docs/migrations.md` - Documentation

**Atlas Commands:**

```bash
# Create migration
atlas migrate diff add_feature_x \
  --dir "file://migrations" \
  --dev-url "docker://postgres/15/dev"

# Apply migrations
atlas migrate apply \
  --dir "file://migrations" \
  --url "$DATABASE_URL"

# Lint migrations
atlas migrate lint \
  --dir "file://migrations" \
  --dev-url "docker://postgres/15/dev"
```

**Install Atlas:**

```bash
curl -sSf https://atlasgo.sh | sh
```

---

## Success Criteria

### Must Have (Blocking)

- ✅ IOTA wallet creation working
- ✅ IOTA payment detection working
- ✅ Videos federate to ActivityPub on upload
- ✅ Structured logging in all modules
- ✅ Prometheus metrics expanded
- ✅ Go-Atlas configured
- ✅ All tests passing
- ✅ Test coverage: 80%+

### Should Have

- ✅ IOTA payment worker deployed
- ✅ Comment federation working
- ✅ OpenTelemetry tracing configured
- ✅ Atlas CI checks enabled

---

## Timeline

**Day 1 (8h):** IOTA schema + client + VideoObject + Atlas config
**Day 2 (8h):** IOTA service + Create activity + Logging + Atlas CI
**Day 3 (8h):** Payment worker + API + Comment federation + Metrics
**Day 4 (6h):** Tracing + Integration tests + Docs

**Total:** 30 hours (~4 days)

---

## Testing Checklist

### IOTA Payments

- ✅ Wallet creation generates unique seed
- ✅ Seed encrypted before storage
- ✅ Address derivation is deterministic
- ✅ Payment intent creates unique address
- ✅ Payment worker detects confirmed transactions
- ✅ Expired intents marked correctly

### ActivityPub

- ✅ VideoObject includes all required fields
- ✅ Create activity delivered to followers
- ✅ Private videos not federated
- ✅ Comments federate as Note objects
- ✅ Videos visible on Mastodon/PeerTube

### Observability

- ✅ All logs use slog with request_id
- ✅ JSON format in production
- ✅ 30+ Prometheus metrics exported
- ✅ Traces visible in Jaeger
- ✅ End-to-end trace for video upload

### Atlas

- ✅ Migrations apply cleanly
- ✅ Lint checks pass
- ✅ CI blocks destructive changes

---

## Risk Mitigation

**IOTA Node Access:**

- Use IOTA testnet (free)
- Mock client for unit tests
- Feature flag: ENABLE_IOTA=false

**OpenTelemetry Backend:**

- Optional feature (OTEL_EXPORTER_OTLP_ENDPOINT)
- Use Jaeger all-in-one for dev
- Graceful degradation if no endpoint

**Test Flakiness:**

- Mock all external HTTP calls
- Use test doubles for IOTA client
- Integration tests optional if services unavailable

---

## Quick Start (For Agents)

1. **Review assigned epic in SPRINT2_EXECUTION_PLAN.md**
2. **Create files as specified in task breakdown**
3. **Follow TDD approach (tests first)**
4. **Ensure test coverage: 80%+**
5. **Request code review from Go Backend Reviewer**
6. **Integrate with existing codebase (no breaking changes)**
7. **Update metrics and logging in all new code**
8. **Document environment variables in .env.example**

---

## Contact

**Sprint Lead:** Decentralized Video PM
**Code Review:** Go Backend Reviewer
**Test Review:** Golang Test Guardian
**Integration Support:** Federation Protocol Auditor

**Questions?** Reference detailed plan in `SPRINT2_EXECUTION_PLAN.md`
