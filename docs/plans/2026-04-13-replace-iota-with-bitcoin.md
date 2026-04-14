# Replace IOTA with Bitcoin (BTCPay Server) Implementation Plan

Created: 2026-04-13
Author: yegamble@gmail.com
Status: PENDING
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

## Summary

**Goal:** Remove all IOTA payment infrastructure from vidra-core and vidra-user, and replace with Bitcoin payments via a self-hosted BTCPay Server running as a Docker Compose service (with NBXplorer + bitcoind regtest).

**Architecture:** BTCPay Server handles all Bitcoin key management, invoice creation, and payment detection via webhooks. Vidra Core acts as a thin integration layer — creating invoices via BTCPay's Greenfield REST API and receiving payment notifications via webhook callbacks. No server-side wallet management or crypto signing needed.

**Tech Stack:** BTCPay Server (Docker), NBXplorer (Bitcoin indexer), bitcoind (regtest), Go HTTP client for BTCPay Greenfield API, webhook handler for payment notifications.

## Scope

### In Scope

- Remove all IOTA code from vidra-core (client, domain models, repository, handlers, worker, health checker, metrics, wallet encryption, config, Docker services, migrations, OpenAPI, Postman)
- Remove IOTA Docker services (iota-node, iota-node-test, iota_data volume, docker/iota/ configs)
- Remove `.claude/rules/iota-guardrails.md`
- Add BTCPay Server + NBXplorer + bitcoind Docker Compose services (dev profile `bitcoin`, test profile `test-integration`)
- New Go BTCPay client (Greenfield REST API)
- New domain models (BTCPayInvoice, BTCPayWebhookEvent)
- New repository for BTCPay invoice/payment tracking
- New HTTP handlers: create invoice, get invoice, list payments, webhook callback
- New Goose migration: drop IOTA tables, create btcpay tables
- Update config (replace IOTA env vars with BTCPay env vars)
- Update OpenAPI spec (`api/openapi_payments.yaml`)
- Update Postman collections
- Update vidra-user frontend: replace IOTA logo/references with Bitcoin, update payment service, update inner-circle component
- Update feature parity registry, CLAUDE.md, README

### Out of Scope

- Lightning Network support (future enhancement)
- Multi-currency support (only Bitcoin for now)
- Production BTCPay Server hardening (TLS, external bitcoind) — this plan covers dev/test
- Payment refund flows (deferred)
- vidra-user admin payments dashboard redesign (keep existing structure, update labels)

## Approach

**Chosen:** Clean replacement — delete all IOTA code, then build BTCPay integration from scratch.

**Why:** The IOTA and Bitcoin payment models are fundamentally different (client-side signing vs server-managed invoices). Attempting an incremental swap would create an awkward hybrid state with no real benefit. Clean replacement is simpler, faster, and produces cleaner code.

**Alternatives considered:**
- Incremental swap: Replace one layer at a time while keeping feature flag working. Rejected — creates hybrid IOTA/Bitcoin state, more error-prone, and the APIs are too different to map cleanly.
- Generic payment abstraction: Build a payment provider interface supporting multiple backends. Rejected — YAGNI, only Bitcoin is needed now. Can abstract later if needed.

## Context for Implementer

> Write for an implementer who has never seen the codebase.

- **Patterns to follow:**
  - Handler pattern: `internal/httpapi/handlers/payments/payment_handlers.go` (being replaced, but structure is canonical)
  - Domain models: `internal/domain/payment.go` — follow same struct tag conventions (`json`, `db`)
  - Repository pattern: `internal/repository/iota_repository.go` — SQLX with `db` tags
  - Service pattern: `internal/usecase/payments/payment_service.go` — constructor DI, context-first
  - Route registration: `internal/httpapi/routes.go:433-442` — conditional on config flag
  - App wiring: `internal/app/app.go:630-652` — conditional IOTA init block is the pattern

- **Conventions:**
  - Response envelope: `shared.WriteJSON(w, status, data)` / `shared.WriteError(w, status, err)`
  - Error handling: Domain errors from `internal/domain/errors.go`, wrapped with `fmt.Errorf`
  - Config: Fields in `internal/config/config.go`, loaded in `config_load.go` via `GetEnvOrDefault`
  - Feature flag: `EnableIOTA` → becomes `EnableBitcoin`

- **Key files being modified:**
  - `internal/app/app.go` — DI wiring (lines 35, 54, 84, 114, 161, 396-397, 630-652, 981, 1040)
  - `internal/config/config.go` — Config struct (lines 54-57, 64)
  - `internal/config/config_load.go` — Env loading (lines 40-43, 48)
  - `internal/httpapi/routes.go` — Route registration (lines 26, 433-442)
  - `internal/httpapi/health.go` — Health checker wiring (lines 49, 57)
  - `internal/health/checker.go` — IOTAChecker (lines 431-490)
  - `internal/obs/metrics.go` + `metrics_test.go` — IOTA metrics
  - `docker-compose.yml` — iota-node (lines 129-148), iota-node-test (345+), volume (741), env vars
  - `.env.example` — IOTA env vars (lines 41-46, 71)

- **Gotchas:**
  - The `migrations/005_add_bitcoin_wallet_to_users.sql` already adds a `bitcoin_wallet` column to users — this migration already exists and should be left as-is
  - Security `wallet_encryption.go` and `wallet_encryption_test.go` are being removed — ensure no other code depends on `WalletEncryptionService`
  - The `internal/obs/metrics.go` has IOTA-specific Prometheus counters that need replacement
  - Docker test profile `test-integration` has `iota-node-test` service and env vars that must be updated
  - vidra-user is a separate repo at `~/github/vidra-user` — not a Go module, it's a Next.js app

- **Domain context:**
  - BTCPay Server Greenfield API: REST API at `https://docs.btcpayserver.org/API/Greenfield/v1/`
  - Key concepts: **Store** (merchant account), **Invoice** (payment request with amount + expiry), **Webhook** (payment notification callback)
  - Flow: Create invoice → user pays to Bitcoin address shown in invoice → BTCPay detects payment → webhook fires → Vidra updates payment status

## Runtime Environment

- **Start command:** `docker compose --profile bitcoin up -d` (BTCPay stack), `make run` (Vidra Core)
- **BTCPay Port:** 14080 (host) → 49392 (container)
- **NBXplorer Port:** 24444 (container, internal only)
- **bitcoind Port:** 18443 (regtest RPC, internal only)
- **Health check:** BTCPay `GET /api/v1/health`
- **Restart:** `docker compose --profile bitcoin restart`

## Feature Inventory

### Files Being REMOVED (IOTA)

| File | Functions/Types | Task # |
|------|----------------|--------|
| `internal/payments/iota_client.go` | IOTAClient, jsonRPCClient, all IOTA RPC methods | Task 1 |
| `internal/payments/iota_client_test.go` | All IOTA client tests | Task 1 |
| `internal/domain/payment.go` | IOTAWallet, IOTAPaymentIntent, IOTATransaction, ReceivedTransaction, domain errors | Task 1 |
| `internal/domain/payment_test.go` | Domain model tests | Task 1 |
| `internal/port/payment.go` | PaymentService interface | Task 1 |
| `internal/repository/iota_repository.go` | IOTARepository (SQLX CRUD) | Task 1 |
| `internal/repository/iota_repository_test.go` | Repository integration tests | Task 1 |
| `internal/repository/iota_repository_unit_test.go` | Repository unit tests | Task 1 |
| `internal/usecase/payments/payment_service.go` | PaymentService (business logic) | Task 1 |
| `internal/usecase/payments/payment_service_test.go` | Service tests | Task 1 |
| `internal/usecase/payments/helpers_test.go` | Test helpers | Task 1 |
| `internal/usecase/payments/payment_service_extended_test.go` | Extended tests | Task 1 |
| `internal/httpapi/handlers/payments/payment_handlers.go` | PaymentHandler (HTTP handlers) | Task 1 |
| `internal/httpapi/handlers/payments/payment_handlers_test.go` | Handler tests | Task 1 |
| `internal/worker/iota_payment_worker.go` | IOTAPaymentWorker | Task 1 |
| `internal/worker/iota_payment_worker_test.go` | Worker tests | Task 1 |
| `internal/security/wallet_encryption.go` | WalletEncryptionService | Task 1 |
| `internal/security/wallet_encryption_test.go` | Encryption tests | Task 1 |
| `docker/iota/fullnode.yaml` | IOTA node config | Task 2 |
| `docker/iota/entrypoint.sh` | IOTA node entrypoint | Task 2 |
| `api/openapi_payments.yaml` | IOTA payments OpenAPI spec | Task 7 |
| `postman/vidra-payments.postman_collection.json` | IOTA payment tests | Task 7 |
| `postman/vidra-e2e-payment-flow.postman_collection.json` | IOTA E2E flow | Task 7 |
| `postman/results-vidra-payments.postman_collection.json` | IOTA results | Task 7 |
| `.claude/rules/iota-guardrails.md` | IOTA development guardrails | Task 8 |

### Files Being MODIFIED

| File | Changes | Task # |
|------|---------|--------|
| `internal/app/app.go` | Replace IOTA wiring with BTCPay wiring | Task 5 |
| `internal/config/config.go` | Replace IOTA config fields with BTCPay fields | Task 3 |
| `internal/config/config_load.go` | Replace IOTA env var loading | Task 3 |
| `internal/config/config_test.go` | Replace IOTA config test | Task 3 |
| `internal/httpapi/routes.go` | Replace IOTA route registration with BTCPay routes | Task 5 |
| `internal/httpapi/health.go` | Replace IOTAChecker with BTCPayChecker | Task 5 |
| `internal/health/checker.go` | Remove IOTAChecker, add BTCPayChecker | Task 5 |
| `internal/obs/metrics.go` | Replace IOTA metrics with Bitcoin/BTCPay metrics | Task 5 |
| `internal/obs/metrics_test.go` | Update metrics tests | Task 5 |
| `docker-compose.yml` | Remove iota-node, add btcpay/nbxplorer/bitcoind services | Task 2 |
| `.env.example` | Replace IOTA env vars with BTCPay vars | Task 3 |
| `scripts/coverage-thresholds.txt` | Update commented payments threshold | Task 7 |

### Files Being CREATED (BTCPay)

| File | Purpose | Task # |
|------|---------|--------|
| `internal/payments/btcpay_client.go` | BTCPay Greenfield API client | Task 3 |
| `internal/payments/btcpay_client_test.go` | Client tests | Task 3 |
| `internal/domain/btcpay.go` | BTCPay domain models | Task 3 |
| `internal/domain/btcpay_test.go` | Domain model tests | Task 3 |
| `internal/port/btcpay.go` | BTCPayService interface | Task 4 |
| `internal/repository/btcpay_repository.go` | BTCPay DB repository | Task 4 |
| `internal/repository/btcpay_repository_test.go` | Repository tests | Task 4 |
| `internal/usecase/payments/btcpay_service.go` | BTCPay business logic | Task 4 |
| `internal/usecase/payments/btcpay_service_test.go` | Service tests | Task 4 |
| `internal/httpapi/handlers/payments/btcpay_handlers.go` | BTCPay HTTP handlers | Task 5 |
| `internal/httpapi/handlers/payments/btcpay_handlers_test.go` | Handler tests | Task 5 |
| `migrations/NNN_drop_iota_add_btcpay.sql` | Drop IOTA tables, create btcpay tables | Task 6 |
| `docker/btcpay/docker-fragment.yml` | BTCPay Docker config (for reference) | Task 2 |

### vidra-user Files Being MODIFIED

| File | Changes | Task # |
|------|---------|--------|
| `src/components/ui/iota-logo.tsx` | Replace with `bitcoin-logo.tsx` (or rename + replace SVG) | Task 8 |
| `src/components/inner-circle.tsx` | Replace iotaPrice→btcPrice, iotaAddress→bitcoinAddress, payment method "iota"→"bitcoin" | Task 8 |
| `src/lib/api/services/payments.ts` | Update API calls to match new BTCPay endpoints | Task 8 |
| `src/lib/api/services/__tests__/payments.test.ts` | Update tests | Task 8 |
| `src/lib/payments/feature-flag.ts` | Update flag name if needed | Task 8 |
| `src/components/pages/__tests__/admin-page-payments.test.tsx` | Update references | Task 8 |
| `src/components/pages/__tests__/settings-page-payments.test.tsx` | Update references | Task 8 |

## Assumptions

- BTCPay Server Docker image `btcpayserver/btcpayserver` is available and stable — supported by [BTCPay Server Docker repo](https://github.com/btcpayserver/btcpayserver-docker). Tasks 2, 3, 4, 5 depend on this.
- BTCPay Greenfield API v1 is the correct API to target — it's the current recommended API per BTCPay docs. Tasks 3, 4, 5 depend on this.
- IOTA was never used in production (feature flag `ENABLE_IOTA` defaults to `false`) — dropping IOTA tables is safe. Task 6 depends on this.
- The existing `migrations/005_add_bitcoin_wallet_to_users.sql` column is unrelated to BTCPay and can remain as-is. Task 6 depends on this.
- vidra-user at `~/github/vidra-user` is a Next.js app using TypeScript. Task 8 depends on this.
- regtest mode is sufficient for development and CI — no external Bitcoin network dependency. Task 2 depends on this.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| BTCPay Server Docker image large (>1GB) | High | Medium — slower CI | Use multi-stage Docker build, cache layers. Pin specific version tag. |
| BTCPay regtest setup complexity | Medium | Medium — dev friction | Provide seed script that auto-creates store + API key on first run |
| Webhook delivery reliability | Medium | High — missed payments | Store webhook events in DB, implement idempotent processing, add retry logic |
| Breaking existing payment feature flag consumers | Low | Medium | Feature flag name changes from `ENABLE_IOTA` to `ENABLE_BITCOIN` — search all consumers |
| BTCPay API version drift | Low | Low | Pin BTCPay Server version in Docker Compose |

## Goal Verification

### Truths

1. `make build` succeeds with zero IOTA references in compiled code
2. `make test` passes with zero IOTA test files remaining
3. `grep -ri 'iota' internal/` returns zero results (excluding comments in migration down blocks)
4. `docker compose --profile bitcoin up -d` starts BTCPay Server, NBXplorer, and bitcoind successfully
5. BTCPay health check (`GET /api/v1/health`) returns 200
6. Creating a BTCPay invoice via `POST /api/v1/payments/invoices` returns a valid invoice with Bitcoin address
7. vidra-user builds without IOTA references and shows Bitcoin payment UI

### Artifacts

1. All Go files in `internal/payments/`, `internal/domain/btcpay*`, `internal/repository/btcpay*`, `internal/usecase/payments/btcpay*`, `internal/httpapi/handlers/payments/btcpay*`
2. Docker Compose services: `btcpay-server`, `nbxplorer`, `bitcoind`
3. Migration file: `migrations/NNN_drop_iota_add_btcpay.sql`
4. OpenAPI spec: `api/openapi_payments.yaml` (rewritten)
5. Postman collections updated in `postman/`

## Progress Tracking

- [x] Task 1: Remove all IOTA code
- [x] Task 2: Add BTCPay Docker services
- [x] Task 3: BTCPay client + domain models + config
- [x] Task 4: BTCPay repository + service layer
- [x] Task 5: BTCPay HTTP handlers + app wiring
- [x] Task 6: Database migration
- [x] Task 7: OpenAPI + Postman + validation
- [ ] Task 8: vidra-user frontend + docs update

**Total Tasks:** 8 | **Completed:** 7 | **Remaining:** 1

## Implementation Tasks

### Task 1: Remove All IOTA Code

**Objective:** Delete every IOTA-specific file and remove IOTA references from shared files. After this task, the project builds with zero IOTA payment code (feature flag and routes will be temporarily broken — fixed in Task 5).

**Dependencies:** None

**Files:**

- Delete: `internal/payments/iota_client.go`
- Delete: `internal/payments/iota_client_test.go`
- Delete: `internal/domain/payment.go`
- Delete: `internal/domain/payment_test.go`
- Delete: `internal/port/payment.go`
- Delete: `internal/repository/iota_repository.go`
- Delete: `internal/repository/iota_repository_test.go`
- Delete: `internal/repository/iota_repository_unit_test.go`
- Delete: `internal/usecase/payments/payment_service.go`
- Delete: `internal/usecase/payments/payment_service_test.go`
- Delete: `internal/usecase/payments/helpers_test.go`
- Delete: `internal/usecase/payments/payment_service_extended_test.go`
- Delete: `internal/httpapi/handlers/payments/payment_handlers.go`
- Delete: `internal/httpapi/handlers/payments/payment_handlers_test.go`
- Delete: `internal/worker/iota_payment_worker.go`
- Delete: `internal/worker/iota_payment_worker_test.go`
- Delete: `internal/security/wallet_encryption.go`
- Delete: `internal/security/wallet_encryption_test.go`
- Modify: `internal/app/app.go` — Remove IOTA imports, fields, and wiring blocks (lines 35, 54, 84, 114, 161, 396-397, 630-652, 981, 1040). Comment out or remove PaymentService reference temporarily.
- Modify: `internal/httpapi/routes.go` — Remove IOTA payment route block (lines 26, 433-442). Comment out payment routes temporarily.
- Modify: `internal/httpapi/health.go` — Remove IOTAChecker references (lines 49, 57)
- Modify: `internal/health/checker.go` — Remove IOTAChecker struct and methods (lines 431-490)
- Modify: `internal/obs/metrics.go` — Remove IOTA-specific Prometheus counters
- Modify: `internal/obs/metrics_test.go` — Remove IOTA metrics tests

**Key Decisions / Notes:**

- After deletion, run `go build ./...` to find any remaining broken imports. Fix all compilation errors.
- The `internal/payments/` directory will be empty after deletion — leave it for Task 3 to populate.
- The `internal/usecase/payments/` directory will be empty — leave for Task 4.
- Domain errors that are IOTA-specific (ErrIOTANodeUnavailable, ErrIOTANodeSyncing, ErrInvalidSeed, etc.) go away with `payment.go`. Generic payment errors (ErrInvalidAmount, ErrPaymentIntentNotFound) will be re-created in Task 3.
- `WalletEncryptionService` is only used by the IOTA payment flow — safe to delete. Verify with grep first.

**Definition of Done:**

- [ ] All IOTA files deleted
- [ ] `go build ./...` succeeds with zero IOTA references
- [ ] `go test -short ./internal/app/... ./internal/httpapi/... ./internal/health/... ./internal/obs/...` passes
- [ ] `grep -ri 'iota' internal/ --include='*.go' | grep -v '_test.go' | grep -v '// '` returns zero results (excluding tests and comments)

**Verify:**

```bash
go build ./... && echo "BUILD OK"
go test -short ./internal/app/... ./internal/httpapi/... ./internal/health/... ./internal/obs/... -count=1
```

---

### Task 2: Add BTCPay Docker Services

**Objective:** Add BTCPay Server + NBXplorer + bitcoind (regtest) to Docker Compose. Remove IOTA Docker services.

**Dependencies:** None (can run in parallel with Task 1)

**Files:**

- Delete: `docker/iota/fullnode.yaml`
- Delete: `docker/iota/entrypoint.sh`
- Create: `docker/btcpay/` directory (if any config files needed)
- Modify: `docker-compose.yml` — Remove `iota-node` service (lines 129-158), `iota-node-test` service (line 345+), `iota_data` volume (line 741), IOTA env vars in vidra-core service. Add `bitcoind`, `nbxplorer`, `btcpay-server` services under `bitcoin` profile. Add test variants under `test-integration` profile.

**Key Decisions / Notes:**

- Use `btcpayserver/btcpayserver:1.14.4` (latest stable), `nicolasdorier/nbxplorer:2.5.12`, `btcpayserver/bitcoin:27.1` images
- bitcoind runs in regtest mode (`-regtest`) for instant block generation
- BTCPay Server env vars: `BTCPAY_POSTGRES`, `BTCPAY_EXPLORERURL`, `BTCPAY_BTCEXPLORERURL`
- NBXplorer connects to bitcoind via RPC
- Port mapping: BTCPay 14080:49392 (host:container), bitcoind 18443 (RPC, internal)
- The `bitcoin` profile replaces the `iota` profile. The `full` profile should include `bitcoin` instead of `iota`.
- BTCPay needs PostgreSQL — reuse the existing `postgres` service (different database name: `btcpay`)
- Health check: `curl -sf http://localhost:49392/api/v1/health`

**Definition of Done:**

- [ ] `docker compose --profile bitcoin up -d` starts all 3 services
- [ ] BTCPay Server health check returns 200
- [ ] `docker compose --profile bitcoin down` cleans up
- [ ] No IOTA Docker references remain in docker-compose.yml
- [ ] `docker/iota/` directory removed

**Verify:**

```bash
docker compose --profile bitcoin up -d
sleep 15
curl -sf http://localhost:14080/api/v1/health && echo "BTCPAY OK"
docker compose --profile bitcoin down
```

---

### Task 3: BTCPay Client + Domain Models + Config

**Objective:** Create the BTCPay Greenfield API client, new domain models, and update config to replace IOTA env vars.

**Dependencies:** Task 1

**Files:**

- Create: `internal/payments/btcpay_client.go` — HTTP client for BTCPay Greenfield API v1
- Create: `internal/payments/btcpay_client_test.go` — Client tests with httptest mock server
- Create: `internal/domain/btcpay.go` — BTCPayInvoice, BTCPayPayment, BTCPayWebhookEvent models + domain errors
- Create: `internal/domain/btcpay_test.go` — Model validation tests
- Modify: `internal/config/config.go` — Replace IOTA fields with: `BTCPayServerURL`, `BTCPayAPIKey`, `BTCPayStoreID`, `BTCPayWebhookSecret`, `EnableBitcoin`
- Modify: `internal/config/config_load.go` — Replace IOTA env loading with BTCPay env loading
- Modify: `internal/config/config_test.go` — Replace IOTA config test with BTCPay config test
- Modify: `.env.example` — Replace IOTA section with BTCPay section

**Key Decisions / Notes:**

- BTCPay Greenfield API endpoints to implement:
  - `POST /api/v1/stores/{storeId}/invoices` — Create invoice
  - `GET /api/v1/stores/{storeId}/invoices/{invoiceId}` — Get invoice
  - `GET /api/v1/stores/{storeId}/invoices` — List invoices
  - `GET /api/v1/health` — Health check
- Auth: `Authorization: token <api-key>` header
- Invoice states: `New`, `Processing`, `Settled`, `Invalid`, `Expired`
- Domain model `BTCPayInvoice` maps to BTCPay's invoice response
- Config env vars: `BTCPAY_SERVER_URL`, `BTCPAY_API_KEY`, `BTCPAY_STORE_ID`, `BTCPAY_WEBHOOK_SECRET`, `ENABLE_BITCOIN`
- Re-create generic payment errors: `ErrInvoiceNotFound`, `ErrInvoiceExpired`, `ErrInvalidAmount`, `ErrBTCPayUnavailable`

**Definition of Done:**

- [ ] BTCPay client creates invoices, gets invoices, checks health via mock HTTP server tests
- [ ] Domain models have proper struct tags and validation
- [ ] Config loads all BTCPay env vars with sensible defaults
- [ ] All tests pass
- [ ] `go build ./...` succeeds

**Verify:**

```bash
go test -short ./internal/payments/... ./internal/domain/... ./internal/config/... -count=1 -v
```

---

### Task 4: BTCPay Repository + Service Layer

**Objective:** Create the database repository for BTCPay invoices/payments and the business logic service.

**Dependencies:** Task 3

**Files:**

- Create: `internal/port/btcpay.go` — BTCPayService interface
- Create: `internal/repository/btcpay_repository.go` — SQLX repository for btcpay_invoices and btcpay_payments tables
- Create: `internal/repository/btcpay_repository_test.go` — Repository tests (sqlmock)
- Create: `internal/usecase/payments/btcpay_service.go` — Business logic: create invoice, process webhook, get payment status
- Create: `internal/usecase/payments/btcpay_service_test.go` — Service tests

**Key Decisions / Notes:**

- `BTCPayService` interface methods:
  - `CreateInvoice(ctx, userID, amount, currency, metadata) (*BTCPayInvoice, error)`
  - `GetInvoice(ctx, invoiceID) (*BTCPayInvoice, error)`
  - `GetPaymentsByUser(ctx, userID, limit, offset) ([]*BTCPayPayment, error)`
  - `ProcessWebhook(ctx, webhookEvent) error`
- Repository stores local copies of invoice data + webhook events for audit trail
- Webhook processing is idempotent — deduplicate by `invoice_id + event_type`
- Service validates webhook signatures using `BTCPAY_WEBHOOK_SECRET` (HMAC-SHA256)

**Definition of Done:**

- [ ] Repository CRUD operations tested with sqlmock
- [ ] Service creates invoices via BTCPay client, stores in DB
- [ ] Webhook processing is idempotent and validates signatures
- [ ] All tests pass
- [ ] `go build ./...` succeeds

**Verify:**

```bash
go test -short ./internal/port/... ./internal/repository/... ./internal/usecase/payments/... -count=1 -v
```

---

### Task 5: BTCPay HTTP Handlers + App Wiring

**Objective:** Create HTTP handlers for BTCPay payment endpoints, wire everything into the app, add health checker and metrics.

**Dependencies:** Task 4

**Files:**

- Create: `internal/httpapi/handlers/payments/btcpay_handlers.go` — HTTP handlers
- Create: `internal/httpapi/handlers/payments/btcpay_handlers_test.go` — Handler tests
- Modify: `internal/app/app.go` — Wire BTCPay client, repository, service, worker (replace old IOTA wiring)
- Modify: `internal/httpapi/routes.go` — Register BTCPay payment routes under `/api/v1/payments/`
- Modify: `internal/httpapi/health.go` — Add BTCPayChecker
- Modify: `internal/health/checker.go` — Add BTCPayChecker struct
- Modify: `internal/obs/metrics.go` — Add Bitcoin/BTCPay Prometheus counters
- Modify: `internal/obs/metrics_test.go` — Update metrics tests

**Key Decisions / Notes:**

- New routes (all under `/api/v1/payments/` with auth middleware):
  - `POST /invoices` — Create payment invoice (amount in sats, optional video_id)
  - `GET /invoices/{id}` — Get invoice status
  - `GET /invoices` — List user's invoices (paginated)
  - `POST /webhooks/btcpay` — BTCPay webhook callback (NO auth middleware — uses HMAC signature verification)
- Health checker: `GET /api/v1/health` includes BTCPay check when `EnableBitcoin` is true
- Metrics: `btcpay_invoices_total{status}`, `btcpay_webhook_events_total{type}`, `btcpay_payment_duration_seconds`
- App wiring follows same conditional pattern: `if app.Config.EnableBitcoin { ... }`

**Definition of Done:**

- [ ] All handler endpoints tested with httptest
- [ ] App wiring compiles and conditionally enables BTCPay
- [ ] Health checker includes BTCPay when enabled
- [ ] Metrics registered for BTCPay operations
- [ ] `make build` succeeds
- [ ] `make test` passes (all unit tests)

**Verify:**

```bash
make build && make test
```

---

### Task 6: Database Migration

**Objective:** Create a Goose migration that drops IOTA tables and creates BTCPay tables.

**Dependencies:** Task 4 (needs to know table schemas)

**Files:**

- Create: `migrations/NNN_drop_iota_add_btcpay.sql` — Next migration number after the highest existing one

**Key Decisions / Notes:**

- Migration UP: Drop `iota_transactions`, `iota_payment_intents`, `iota_wallets` (in order due to FK constraints). Create `btcpay_invoices` and `btcpay_payments` tables.
- Migration DOWN: Drop `btcpay_payments`, `btcpay_invoices`. Recreate IOTA tables (copy from migration 062).
- `btcpay_invoices` schema: `id UUID PK`, `btcpay_invoice_id VARCHAR UNIQUE`, `user_id UUID FK→users`, `amount_sats BIGINT`, `currency VARCHAR(10)`, `status VARCHAR(20)`, `btcpay_checkout_link TEXT`, `bitcoin_address VARCHAR(62)`, `metadata JSONB`, `expires_at TIMESTAMP`, `settled_at TIMESTAMP`, `created_at`, `updated_at`
- `btcpay_payments` schema: `id UUID PK`, `invoice_id UUID FK→btcpay_invoices`, `btcpay_payment_id VARCHAR`, `amount_sats BIGINT`, `status VARCHAR(20)`, `transaction_id VARCHAR`, `block_height BIGINT`, `created_at`
- Indexes on `user_id`, `btcpay_invoice_id`, `status`, `expires_at`

**Definition of Done:**

- [ ] Migration has both UP and DOWN sections
- [ ] UP drops IOTA tables, creates BTCPay tables
- [ ] DOWN drops BTCPay tables, recreates IOTA tables
- [ ] `make migrate-up` applies cleanly (on a fresh DB or after existing migrations)
- [ ] `make migrate-down` reverts cleanly

**Verify:**

```bash
make migrate-up && make migrate-status
```

---

### Task 7: OpenAPI + Postman + Validation

**Objective:** Update OpenAPI spec, Postman collections, and run full validation.

**Dependencies:** Task 5

**Files:**

- Rewrite: `api/openapi_payments.yaml` — New BTCPay payment endpoints
- Rewrite: `postman/vidra-payments.postman_collection.json` — BTCPay payment tests
- Rewrite: `postman/vidra-e2e-payment-flow.postman_collection.json` — BTCPay E2E flow
- Delete: `postman/results-vidra-payments.postman_collection.json` — Generated results file
- Modify: `scripts/coverage-thresholds.txt` — Update payments package reference

**Key Decisions / Notes:**

- OpenAPI spec endpoints mirror the handler routes from Task 5
- Postman collection includes: create invoice, get invoice, list invoices, webhook simulation
- Run `make generate-openapi` after spec changes
- Run `make verify-openapi` to check for drift

**Definition of Done:**

- [ ] OpenAPI spec matches implemented endpoints
- [ ] `make generate-openapi` succeeds
- [ ] `make verify-openapi` passes
- [ ] Postman collections updated with BTCPay requests
- [ ] `make validate-all` passes

**Verify:**

```bash
make validate-all
```

---

### Task 8: vidra-user Frontend + Documentation

**Objective:** Update vidra-user frontend to replace IOTA references with Bitcoin, and update all project documentation.

**Dependencies:** Task 5 (API endpoints must be finalized)

**Files:**

- Modify: `~/github/vidra-user/src/components/ui/iota-logo.tsx` — Rename to `bitcoin-logo.tsx`, replace SVG with Bitcoin logo
- Modify: `~/github/vidra-user/src/components/inner-circle.tsx` — Replace `iotaPrice` → `btcPrice`, `iotaAddress` → `bitcoinAddress`, payment method `"iota"` → `"bitcoin"`, import `BitcoinLogo` instead of `IotaLogo`
- Modify: `~/github/vidra-user/src/lib/api/services/payments.ts` — Update API calls to new BTCPay endpoints (`/invoices` instead of `/wallet`, `/intents`)
- Modify: `~/github/vidra-user/src/lib/api/services/__tests__/payments.test.ts` — Update tests
- Modify: `~/github/vidra-user/src/lib/payments/feature-flag.ts` — Update flag if needed
- Modify: `~/github/vidra-user/src/components/pages/__tests__/admin-page-payments.test.tsx` — Update references
- Modify: `~/github/vidra-user/src/components/pages/__tests__/settings-page-payments.test.tsx` — Update references
- Delete: `.claude/rules/iota-guardrails.md` — No longer relevant
- Modify: `.claude/rules/feature-parity-registry.md` — Update IOTA row to Bitcoin/BTCPay
- Modify: `CLAUDE.md` — Update payment references
- Modify: `README.md` — Update payment feature description

**Key Decisions / Notes:**

- Bitcoin SVG logo: Use standard Bitcoin "₿" mark
- The inner-circle component has hardcoded tier prices in `iotaPrice` — these become `btcPrice` (values will need adjustment since BTC is priced differently than IOTA)
- Feature flag `isPaymentsEnabled` should check for `ENABLE_BITCOIN` or equivalent
- vidra-user tests may need `npm test` or `bun test` to verify

**Definition of Done:**

- [ ] No IOTA references in vidra-user source
- [ ] vidra-user builds successfully
- [ ] All vidra-user tests pass
- [ ] `iota-guardrails.md` deleted
- [ ] Feature parity registry updated (IOTA → Bitcoin/BTCPay)
- [ ] CLAUDE.md and README updated

**Verify:**

```bash
cd ~/github/vidra-user && npm run build && npm test
cd ~/github/vidra-core && grep -ri 'iota' .claude/rules/ --include='*.md' | grep -v 'feature-parity'
```

## Open Questions

1. **BTCPay API key provisioning:** Should a setup script auto-create the BTCPay store and API key on first `docker compose up`, or should this be manual? (Recommendation: auto-create via init script for dev, manual for prod)

## Deferred Ideas

- **Lightning Network support** — BTCPay Server supports Lightning out of the box. Can be enabled later by adding LND/CLN container.
- **Payment refund flow** — BTCPay supports refunds via `POST /api/v1/stores/{storeId}/invoices/{invoiceId}/refund`
- **Multi-currency** — BTCPay supports altcoins. Could add Litecoin, Monero later.
- **Payment notifications** — Push notifications to users when payment status changes
- **BTCPay admin dashboard proxy** — Expose BTCPay's built-in admin UI through Vidra
