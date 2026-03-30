# IOTA Transaction-Based Payment Detection

Created: 2026-03-29
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

## Summary

**Goal:** Replace balance-based payment detection with transaction-based detection via `iotax_queryTransactionBlocks` JSON-RPC, eliminating race conditions when multiple payments target the same address.

**Architecture:** Add `QueryTransactionBlocks` to the IOTAClient interfaces (both `IOTANodeClient` internal and consumer-facing). The `jsonRPCClient` calls `iotax_queryTransactionBlocks` with a `ToAddress` filter and `showBalanceChanges: true`. Detection logic filters client-side by timestamp (only transactions after intent creation, with 5s buffer for clock skew) and sums IOTA balance changes. Real transaction digests replace synthetic ones.

**Tech Stack:** Go, IOTA Rebased JSON-RPC (`iotax_queryTransactionBlocks`), existing testify/mock test infrastructure.

## Scope

### In Scope

- Add `QueryTransactionBlocks` RPC method to `jsonRPCClient` and `IOTAClient`
- Rewrite `PaymentService.DetectPayment` to use transaction queries
- Rewrite `IOTAPaymentWorker.checkPaymentIntent` to use transaction queries
- Update all unit tests for both detection paths
- Update mock RPC server to handle `iotax_queryTransactionBlocks`
- Remove the TODO comment at `payment_service.go:246`

### Out of Scope

- Unique payment addresses per intent (separate architectural change)
- Cursor-based pagination / stored cursor tracking
- Digest-based deduplication against DB (additive future enhancement)
- Changes to `GetBalance` or `GetWalletBalance` methods (still used for wallet balance display)

## Approach

**Chosen:** Extend IOTAClient interfaces directly
**Why:** Simplest approach, follows existing codebase patterns (e.g., how `GetBalance` is surfaced). Mechanical mock updates are the only cost.
**Alternatives considered:**
- Shared TransactionDetector component — adds indirection for two slightly different detection paths; premature abstraction
- Lower-level IOTANodeClient only — better layering but more interfaces to thread through

## Context for Implementer

> Write for an implementer who has never seen the codebase.

- **Patterns to follow:**
  - RPC method implementation: see `jsonRPCClient.GetAddressBalance` at `internal/payments/iota_client.go:413-424` — define response struct, call `callRPC`, parse result
  - Interface layering: `IOTANodeClient` (raw RPC, internal) → `IOTAClient` struct (wrapper) → consumer interfaces in `payment_service.go` and `iota_payment_worker.go`
  - The `TransactionBuilder` interface pattern at `iota_client.go:51-55` shows how optional capabilities are added as separate interfaces, but we're adding to `IOTANodeClient` directly since this is a core read operation

- **Conventions:**
  - Extended IOTA RPC methods use `iotax_` prefix (not `suix_`): see `iotax_getBalance`, `iotax_getCoins`
  - Core methods use `iota_` prefix: see `iota_getTransactionBlock`, `iota_executeTransactionBlock`
  - Error wrapping: `fmt.Errorf("context: %w", err)`
  - Response types are package-private (lowercase) when only used internally by `jsonRPCClient`; exported when part of the public API

- **Key files:**
  - `internal/payments/iota_client.go` — concrete IOTA client with all RPC methods
  - `internal/usecase/payments/payment_service.go` — payment service with `DetectPayment` (line 218-279)
  - `internal/worker/iota_payment_worker.go` — worker with `checkPaymentIntent` (line 104-148)
  - `internal/domain/payment.go` — domain types (IOTAPaymentIntent, IOTATransaction, etc.)
  - `tests/mocks/iota-rpc/main.go` — mock IOTA JSON-RPC server

- **Gotchas:**
  - `IOTAClient` interface is defined separately in 3 places: `payment_service.go:35-41`, `iota_payment_worker.go:27-29`, and `iota_client.go:33-38` (as `IOTANodeClient`). All three need updating.
  - Mock types are defined locally in each test file (not shared). `MockIOTAClient` in `payment_service_test.go`, `MockIOTAPaymentClient` in `iota_payment_worker_test.go`, `MockIOTANodeClient` in `iota_client_test.go`.
  - The worker's `IOTAClient` interface is minimal (`GetBalance` only). Adding `QueryTransactionBlocks` is the first significant expansion.
  - `DetectPayment` creates a synthetic `txDigest` (`iota-payment-<id>-<timestamp>`). The new version should use the real digest from the query response.
  - The `IOTAPaymentIntent.CreatedAt` field provides the timestamp for filtering.

- **Domain context:**
  - A `PaymentIntent` is created when a user initiates a payment. It has a `PaymentAddress` (the creator's wallet address), an `AmountIOTA`, and an `ExpiresAt`.
  - `DetectPayment` is called to check if a specific intent has been fulfilled.
  - `checkPaymentIntent` is called by the worker's polling loop for all active intents.
  - Both currently compare address balance against expected amount. The race condition: if two intents share the same `PaymentAddress`, a single payment could satisfy the wrong intent, or both intents could see the same balance increase and both mark as paid.

## Assumptions

- IOTA Rebased nodes support `iotax_queryTransactionBlocks` with `ToAddress` filter and `showBalanceChanges` option — supported by the Sui-fork origin where `suix_queryTransactionBlocks` is a standard indexer method. Tasks 1, 2, 3 depend on this.
- The `timestampMs` field in query results is in Unix milliseconds — supported by Sui/IOTA Rebased API convention. Tasks 2, 3 depend on this.
- Balance changes include the coin type `0x2::iota::IOTA` for native IOTA transfers — supported by the existing `iotax_getBalance` using the same coin type at `iota_client.go:414`. Tasks 2, 3 depend on this.
- The `amount` field in `balanceChanges` is a string representation of a signed int64 (positive for receives) — supported by Sui/IOTA Rebased convention. Tasks 2, 3 depend on this.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| IOTA node doesn't support `iotax_queryTransactionBlocks` | Low | High | Verify against IOTA Rebased docs; the method is standard in Sui-based chains. If unavailable, Task 1 test against live testnet will catch it immediately. |
| Indexer lag causes delayed detection | Medium | Low | No fallback by design — next poll cycle will detect. Document that detection latency depends on indexer freshness. |
| Clock skew between node and server exceeds 5s buffer | Low | Low | 5s buffer is generous for well-configured infrastructure. If hit, increase buffer — it's a constant, not hardcoded in multiple places. |
| Multiple transactions summing to the required amount | Low | Medium | Sum all qualifying balance changes. This is actually better than balance-based detection which can't distinguish sources. |

## Goal Verification

### Truths

1. `PaymentService.DetectPayment` uses `iotax_queryTransactionBlocks` instead of `GetBalance` for payment detection
2. `IOTAPaymentWorker.checkPaymentIntent` uses `iotax_queryTransactionBlocks` instead of `GetBalance` for payment detection
3. Detected payments record real transaction digests from the blockchain, not synthetic ones
4. Transactions are filtered by timestamp to only match those after the intent was created
5. All existing tests pass with updated mocks; new tests cover transaction-based detection
6. The TODO comment at `payment_service.go:246` is removed

### Artifacts

1. `internal/payments/iota_client.go` — contains `QueryTransactionBlocks` implementation
2. `internal/usecase/payments/payment_service.go` — updated `DetectPayment` method
3. `internal/worker/iota_payment_worker.go` — updated `checkPaymentIntent` method
4. `internal/payments/iota_client_test.go` — tests for `QueryTransactionBlocks`
5. `internal/usecase/payments/payment_service_test.go` — updated detection tests
6. `internal/worker/iota_payment_worker_test.go` — updated worker detection tests

## Progress Tracking

- [x] Task 1: Add QueryTransactionBlocks to IOTAClient
- [x] Task 2: Switch PaymentService.DetectPayment to transaction-based
- [x] Task 3: Switch Worker.checkPaymentIntent to transaction-based
- [x] Task 4: Update mock RPC server

**Total Tasks:** 4 | **Completed:** 4 | **Remaining:** 0

## Implementation Tasks

### Task 1: Add QueryTransactionBlocks to IOTAClient

**Objective:** Add the `iotax_queryTransactionBlocks` RPC capability to the IOTA client layer, including types, node client interface, RPC implementation, and public wrapper.
**Dependencies:** None
**Mapped Scenarios:** None

**Files:**

- Modify: `internal/payments/iota_client.go`
- Test: `internal/payments/iota_client_test.go`

**Key Decisions / Notes:**

- Add these types to `iota_client.go` alongside existing types (after line 556):
  ```go
  // ReceivedTransaction represents a transaction received at a specific address.
  type ReceivedTransaction struct {
      Digest      string
      TimestampMs int64
      AmountIOTA  int64
  }
  ```
- Add internal response types for JSON unmarshaling (package-private):
  ```go
  type queryTxBlocksResponse struct {
      Data        []queryTxBlockEntry `json:"data"`
      NextCursor  *string             `json:"nextCursor"`
      HasNextPage bool                `json:"hasNextPage"`
  }
  type queryTxBlockEntry struct {
      Digest         string              `json:"digest"`
      TimestampMs    string              `json:"timestampMs"`
      BalanceChanges []balanceChangeEntry `json:"balanceChanges"`
  }
  type balanceChangeEntry struct {
      Owner    balanceChangeOwner `json:"owner"`
      CoinType string            `json:"coinType"`
      Amount   string            `json:"amount"`
  }
  type balanceChangeOwner struct {
      AddressOwner string `json:"AddressOwner"`
  }
  ```
- Add `QueryTransactionBlocks` to `IOTANodeClient` interface (line 33-38):
  ```go
  QueryTransactionBlocks(ctx context.Context, toAddress string, limit int) ([]ReceivedTransaction, error)
  ```
- Implement on `jsonRPCClient` — call `iotax_queryTransactionBlocks` with params:
  ```go
  params := []interface{}{
      map[string]interface{}{
          "filter":  map[string]string{"ToAddress": toAddress},
          "options": map[string]bool{"showBalanceChanges": true},
      },
      nil,   // cursor (start from beginning)
      limit, // limit
      true,  // descending_order (newest first)
  }
  ```
- Parse response: iterate `balanceChanges`, sum positive IOTA amounts where `owner.AddressOwner == toAddress` and `coinType == "0x2::iota::IOTA"`
- Add wrapper on `IOTAClient` struct:
  ```go
  func (c *IOTAClient) QueryTransactionBlocks(ctx context.Context, toAddress string, limit int) ([]ReceivedTransaction, error)
  ```
- Update `MockIOTANodeClient` in test file to implement the new interface method

**Definition of Done:**

- [ ] `IOTANodeClient` interface includes `QueryTransactionBlocks`
- [ ] `jsonRPCClient` implements `iotax_queryTransactionBlocks` RPC call
- [ ] `IOTAClient` struct has public `QueryTransactionBlocks` wrapper
- [ ] Unit test: mock-based test verifies correct parsing of response
- [ ] Unit test: httptest server verifies correct RPC method name and params
- [ ] Unit test: error handling (network error, RPC error, empty results)
- [ ] All existing iota_client tests still pass

**Verify:**

- `go test ./internal/payments/ -run TestIOTAClient -v -count=1`

---

### Task 2: Switch PaymentService.DetectPayment to transaction-based detection

**Objective:** Replace balance-based detection in `DetectPayment` with transaction query, using timestamp filtering and real transaction digests.
**Dependencies:** Task 1
**Mapped Scenarios:** None

**Files:**

- Modify: `internal/usecase/payments/payment_service.go`
- Test: `internal/usecase/payments/payment_service_test.go`
- Test: `internal/usecase/payments/payment_service_extended_test.go`

**Key Decisions / Notes:**

- Add to `IOTAClient` interface at `payment_service.go:35-41`:
  ```go
  QueryTransactionBlocks(ctx context.Context, toAddress string, limit int) ([]payments.ReceivedTransaction, error)
  ```
  Note: Import `vidra-core/internal/payments` for the `ReceivedTransaction` type. The existing code already imports `vidra-core/internal/domain` so adding a sibling package import follows the same pattern.
- Rewrite `DetectPayment` (lines 218-279):
  1. Keep existing guards: intent lookup, already-paid check, expiry check
  2. Replace balance fetch + delta comparison with:
     ```go
     txs, err := s.client.QueryTransactionBlocks(ctx, intent.PaymentAddress, 50)
     ```
  3. Filter by timestamp: `tx.TimestampMs >= (intent.CreatedAt.UnixMilli() - 5000)` (5s buffer)
  4. Sum `AmountIOTA` from matching transactions
  5. If sum >= `intent.AmountIOTA`, mark as paid
  6. Use the first (most recent) matching transaction's `Digest` as the real `txDigest` instead of synthetic
  7. Still create the `IOTATransaction` record with real digest
  8. Still update wallet balance if wallet exists (fetch current balance separately)
- Remove the TODO comment at line 246
- Remove the `GetBalance` call from DetectPayment (it's no longer needed for detection; `GetWalletBalance` method still uses it independently)
- Update `MockIOTAClient` in test files: add `QueryTransactionBlocks` method
- Update test cases in `TestPaymentService_DetectPayment`:
  - "detect exact payment" → mock `QueryTransactionBlocks` returning a matching transaction
  - "overpayment accepted" → mock returning transaction with higher amount
  - "partial payment - not enough" → mock returning transaction with insufficient amount
  - "intent already paid" → no change (guard before query)
  - "intent expired" → no change (guard before query)
- Update extended test cases:
  - `TestPaymentService_DetectPayment_BalanceCheckError` → rename to `_QueryError`, mock `QueryTransactionBlocks` error
  - `TestPaymentService_DetectPayment_CreateTransactionError` → update mock to use `QueryTransactionBlocks`
  - `TestPaymentService_DetectPayment_UpdateStatusError` → update mock to use `QueryTransactionBlocks`
- Add new test: "no matching transactions after intent creation" → mock returns transactions with old timestamps

**Definition of Done:**

- [ ] `IOTAClient` interface in `payment_service.go` includes `QueryTransactionBlocks`
- [ ] `DetectPayment` uses `QueryTransactionBlocks` instead of `GetBalance`
- [ ] Transactions are filtered by `intent.CreatedAt` with 5s buffer
- [ ] Real transaction digest from query response is stored (not synthetic)
- [ ] TODO comment removed
- [ ] All DetectPayment tests pass with updated mocks
- [ ] New test covers "no transactions after intent creation" case
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/usecase/payments/ -run TestPaymentService_DetectPayment -v -count=1`
- `go test ./internal/usecase/payments/ -v -count=1`

---

### Task 3: Switch Worker.checkPaymentIntent to transaction-based detection

**Objective:** Replace balance-based detection in the worker's `checkPaymentIntent` with transaction query, consistent with Task 2's approach.
**Dependencies:** Task 1
**Mapped Scenarios:** None

**Files:**

- Modify: `internal/worker/iota_payment_worker.go`
- Test: `internal/worker/iota_payment_worker_test.go`

**Key Decisions / Notes:**

- Expand the worker's `IOTAClient` interface at `iota_payment_worker.go:27-29`:
  ```go
  type IOTAClient interface {
      GetBalance(ctx context.Context, address string) (int64, error)
      QueryTransactionBlocks(ctx context.Context, toAddress string, limit int) ([]payments.ReceivedTransaction, error)
  }
  ```
  Import `vidra-core/internal/payments` for the `ReceivedTransaction` type.
- Rewrite `checkPaymentIntent` (lines 104-148):
  1. Replace `GetBalance` with `QueryTransactionBlocks(ctx, intent.PaymentAddress, 50)`
  2. Filter by `intent.CreatedAt` timestamp with 5s buffer (same logic as Task 2)
  3. Sum matching IOTA amounts
  4. If sum >= `intent.AmountIOTA`, create transaction record with real digest and mark as paid
  5. Keep wallet lookup and wallet ID assignment
- Update `MockIOTAPaymentClient` in test file: add `QueryTransactionBlocks` method
- Update `TestIOTAPaymentWorker_CheckPaymentIntent` test cases:
  - "payment detected - exact amount" → mock `QueryTransactionBlocks` with matching tx
  - "payment detected - overpayment" → mock with higher amount
  - "partial payment - not enough" → mock with insufficient amount
  - "network error checking balance" → rename to "network error querying transactions", mock `QueryTransactionBlocks` error
- Update `TestIOTAPaymentWorker_ProcessPayments` tests similarly
- Update `TestIOTAPaymentWorker_ErrorHandling` tests

**Definition of Done:**

- [ ] Worker's `IOTAClient` interface includes `QueryTransactionBlocks`
- [ ] `checkPaymentIntent` uses `QueryTransactionBlocks` instead of `GetBalance`
- [ ] Timestamp filtering matches Task 2's approach
- [ ] Real transaction digests stored
- [ ] All worker tests pass with updated mocks
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/worker/ -run TestIOTAPaymentWorker -v -count=1`

---

### Task 4: Update mock RPC server

**Objective:** Add `iotax_queryTransactionBlocks` handler to the mock IOTA JSON-RPC server used in integration tests.
**Dependencies:** None (can be done in parallel with Tasks 2-3)
**Mapped Scenarios:** None

**Files:**

- Modify: `tests/mocks/iota-rpc/main.go`

**Key Decisions / Notes:**

- Add a case in `handleRPC` switch for `"iotax_queryTransactionBlocks"`:
  ```go
  case "iotax_queryTransactionBlocks":
      resp.Result = map[string]interface{}{
          "data": []map[string]interface{}{
              {
                  "digest":      "MOCK_TX_DIGEST_001",
                  "timestampMs": fmt.Sprintf("%d", time.Now().UnixMilli()),
                  "balanceChanges": []map[string]interface{}{
                      {
                          "owner":    map[string]string{"AddressOwner": "0x1234"},
                          "coinType": "0x2::iota::IOTA",
                          "amount":   "1000000000",
                      },
                  },
              },
          },
          "nextCursor":  nil,
          "hasNextPage": false,
      }
  ```
- Follow the existing pattern of returning static mock data (same as other handlers in the file)

**Definition of Done:**

- [ ] `iotax_queryTransactionBlocks` handler returns a valid mock response
- [ ] Mock server compiles and existing test passes
- [ ] No diagnostics errors

**Verify:**

- `go test ./tests/mocks/iota-rpc/ -v -count=1`
- `go build ./tests/mocks/iota-rpc/`

## Open Questions

None — all key decisions resolved during planning.

### Deferred Ideas

- **Unique payment addresses per intent:** Would eliminate the multi-payment race condition at the address level, but requires HD wallet derivation and address management infrastructure.
- **Digest-based deduplication:** Check transaction digests against the DB to prevent double-matching. Additive enhancement if timestamp filtering proves insufficient for high-frequency payment scenarios.
- **Cursor-based incremental queries:** Store the last cursor per address to avoid re-scanning old transactions. Useful for addresses with high transaction volume.
