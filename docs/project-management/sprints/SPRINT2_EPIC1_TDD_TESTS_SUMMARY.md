# Sprint 2 - Epic 1: IOTA Payments TDD Tests Summary

## Overview

Comprehensive Test-Driven Development (TDD) tests for IOTA payment functionality, written BEFORE implementation. All tests are designed to FAIL initially (RED phase) and will pass once implementation is complete (GREEN phase).

## Test Files Created

### 1. Domain Models

**File:** `/home/user/vidra/internal/domain/payment.go`

- `IOTAWallet` - User wallet with encrypted seed storage
- `IOTAPaymentIntent` - Payment request tracking
- `IOTATransaction` - Blockchain transaction records
- Payment-specific error types
- Security: Seeds never exposed in JSON serialization

### 2. Repository Tests (Database Layer)

**File:** `/home/user/vidra/internal/repository/iota_repository_test.go`
**Test Count:** 20+ test cases

#### Wallet CRUD Tests

- ✓ Create wallet with encrypted seed
- ✓ Retrieve wallet by user_id
- ✓ Retrieve wallet by wallet ID
- ✓ Update wallet balance
- ✓ Handle duplicate wallet creation (constraint violation)
- ✓ Handle non-existent user references
- ✓ Verify encrypted seed is retrieved (never plaintext)

#### Payment Intent Tests

- ✓ Create payment intent with/without video reference
- ✓ Retrieve payment intent by ID
- ✓ Update payment intent status (pending → paid/expired)
- ✓ Link transaction to payment intent
- ✓ Get active payment intents (pending + not expired)
- ✓ Get expired payment intents

#### Transaction Tests

- ✓ Create transaction (deposit/withdrawal/payment types)
- ✓ Retrieve transaction by hash (unique constraint)
- ✓ Update transaction status with confirmations
- ✓ Get transaction history with pagination
- ✓ Track confirmation progress

#### Security Tests

- ✓ Encrypted seed never exposed in logs or responses
- ✓ Seed storage uses AES-256-GCM encryption

### 3. IOTA Client Tests (Node Interaction)

**File:** `/home/user/vidra/internal/payments/iota_client_test.go`
**Test Count:** 25+ test cases

#### Wallet Generation Tests

- ✓ Generate cryptographically secure 256-bit seed
- ✓ Verify seeds are unique (100 iterations)
- ✓ Derive deterministic addresses from seed
- ✓ Different indexes produce different addresses
- ✓ Validate address format (Bech32 with iota1 prefix)
- ✓ Reject invalid seed length/characters

#### Transaction Building Tests

- ✓ Build valid transaction
- ✓ Reject zero/negative amounts
- ✓ Validate from/to addresses
- ✓ Sign transaction with seed
- ✓ Reject invalid seeds for signing

#### Network Operations Tests

- ✓ Query address balance
- ✓ Submit signed transaction
- ✓ Get transaction status (confirmations)
- ✓ Wait for confirmation with polling
- ✓ Handle network errors gracefully
- ✓ Respect context timeouts/cancellation

**Mocking:** All IOTA node interactions are mocked - NO actual network calls in tests.

### 4. Payment Service Tests (Business Logic)

**File:** `/home/user/vidra/internal/usecase/payments/payment_service_test.go`
**Test Count:** 20+ test cases

#### Wallet Management Tests

- ✓ Create wallet for user
- ✓ Prevent duplicate wallet creation
- ✓ Get wallet balance
- ✓ Handle wallet not found errors
- ✓ Encrypt seed with AES-256-GCM before storage
- ✓ Different nonces produce different ciphertexts

#### Payment Intent Tests

- ✓ Create intent with amount and optional video ID
- ✓ Generate unique payment address per intent
- ✓ Set expiration (default 1 hour)
- ✓ Reject invalid amounts (zero/negative)
- ✓ Get payment intent by ID

#### Payment Detection Tests

- ✓ Detect exact payment amount
- ✓ Accept overpayments
- ✓ Handle partial payments (incomplete)
- ✓ Reject already-paid intents
- ✓ Reject expired intents
- ✓ Create transaction record on payment

#### Payment Expiration Tests

- ✓ Expire intents after timeout
- ✓ Update status to expired
- ✓ Handle multiple expired intents

#### Security Tests

- ✓ Seed encryption/decryption roundtrip
- ✓ Seed NEVER logged or exposed (verified in mocks)
- ✓ Different encryptions of same seed produce different ciphertexts

#### Transaction History Tests

- ✓ Get transaction history with pagination
- ✓ Handle wallet not found

### 5. API Handler Tests (HTTP Layer)

**File:** `/home/user/vidra/internal/httpapi/handlers/payments/payment_handlers_test.go`
**Test Count:** 20+ test cases

#### Endpoint Tests

**POST /api/v1/payments/wallet**

- ✓ Create wallet (201 Created)
- ✓ Wallet already exists (409 Conflict)
- ✓ Unauthenticated request (401 Unauthorized)
- ✓ Verify encrypted seed NOT in response

**GET /api/v1/payments/wallet**

- ✓ Get existing wallet (200 OK)
- ✓ Wallet not found (404 Not Found)
- ✓ Unauthenticated (401)

**POST /api/v1/payments/intent**

- ✓ Create intent (201 Created)
- ✓ Invalid amount - zero (400 Bad Request)
- ✓ Invalid amount - negative (400)
- ✓ Missing amount field (400)
- ✓ Unauthenticated (401)

**GET /api/v1/payments/intent/:id**

- ✓ Get existing intent (200 OK)
- ✓ Intent not found (404)
- ✓ Unauthenticated (401)

**GET /api/v1/payments/transactions**

- ✓ Get transaction history (200 OK)
- ✓ Pagination support
- ✓ Wallet not found (404)
- ✓ Unauthenticated (401)

#### Security Tests

- ✓ Input validation (reject invalid amounts)
- ✓ SQL injection prevention (video_id validation)
- ✓ XSS prevention (metadata sanitization)
- ✓ Error message sanitization (no internal details leaked)
- ✓ Security headers verification

### 6. Worker Tests (Background Processing)

**File:** `/home/user/vidra/internal/worker/iota_payment_worker_test.go`
**Test Count:** 15+ test cases

#### Payment Monitoring Tests

- ✓ Poll active payment intents
- ✓ Check balance on payment addresses
- ✓ Detect exact payment amount
- ✓ Detect overpayment (accept)
- ✓ Detect partial payment (wait for completion)
- ✓ Update intent status on payment detection
- ✓ Create transaction record

#### Confirmation Tracking Tests

- ✓ Track transaction confirmations
- ✓ Update confirmation count
- ✓ Mark as confirmed after threshold (10 confirmations)
- ✓ Handle pending confirmations

#### Expiration Tests

- ✓ Expire old payment intents
- ✓ Update multiple expired intents
- ✓ Handle no expired intents gracefully

#### Error Handling Tests

- ✓ Retry on network errors
- ✓ Handle database errors
- ✓ Exponential backoff (design)
- ✓ Max retry limit enforcement

#### Worker Lifecycle Tests

- ✓ Start worker
- ✓ Stop worker gracefully
- ✓ Handle context cancellation
- ✓ Poll interval configuration

## Test Coverage Summary

| Component | File | Test Cases | Key Focus |
|-----------|------|------------|-----------|
| Domain Models | `payment.go` | N/A | Type definitions, error types |
| Repository | `iota_repository_test.go` | 20+ | Database CRUD, constraints, indexes |
| IOTA Client | `iota_client_test.go` | 25+ | Seed generation, address derivation, network ops |
| Payment Service | `payment_service_test.go` | 20+ | Business logic, encryption, payment detection |
| API Handlers | `payment_handlers_test.go` | 20+ | HTTP endpoints, auth, validation, errors |
| Worker | `iota_payment_worker_test.go` | 15+ | Background jobs, polling, confirmations |
| **TOTAL** | | **100+** | **Full stack coverage** |

## Expected Database Schema

The tests expect the following tables (to be created in Part 2):

```sql
-- IOTA Wallets
CREATE TABLE iota_wallets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL UNIQUE REFERENCES users(id),
    encrypted_seed BYTEA NOT NULL,
    seed_nonce BYTEA NOT NULL,
    address TEXT NOT NULL,
    balance_iota BIGINT DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Payment Intents
CREATE TABLE iota_payment_intents (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id),
    video_id UUID REFERENCES videos(id),
    amount_iota BIGINT NOT NULL,
    payment_address TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'paid', 'expired', 'refunded')),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    paid_at TIMESTAMP WITH TIME ZONE,
    transaction_id UUID REFERENCES iota_transactions(id),
    metadata JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Transactions
CREATE TABLE iota_transactions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    wallet_id UUID REFERENCES iota_wallets(id),
    transaction_hash TEXT UNIQUE NOT NULL,
    amount_iota BIGINT NOT NULL,
    tx_type TEXT NOT NULL CHECK (tx_type IN ('deposit', 'withdrawal', 'payment')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'confirmed', 'failed')),
    confirmations INT DEFAULT 0,
    from_address TEXT,
    to_address TEXT,
    metadata JSONB,
    confirmed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
```

## Security Features Tested

1. **Seed Encryption:**
   - AES-256-GCM encryption
   - Unique nonce per encryption
   - Never stored in plaintext
   - Never exposed in logs or JSON responses

2. **Input Validation:**
   - Amount validation (positive, non-zero)
   - Address format validation (Bech32)
   - SQL injection prevention
   - XSS prevention

3. **Authentication:**
   - All endpoints require authentication
   - User context verification

4. **Error Handling:**
   - No internal details in error messages
   - Proper HTTP status codes
   - Sanitized error responses

## Test Execution

### Run All Tests

```bash
go test ./internal/repository -run TestIOTARepository
go test ./internal/payments -run TestIOTAClient
go test ./internal/usecase/payments -run TestPaymentService
go test ./internal/httpapi/handlers/payments -run Test
go test ./internal/worker -run TestIOTAPaymentWorker
```

### Run with Coverage

```bash
go test -cover ./internal/repository/iota_repository_test.go
go test -cover ./internal/payments/iota_client_test.go
go test -cover ./internal/usecase/payments/payment_service_test.go
go test -cover ./internal/httpapi/handlers/payments/payment_handlers_test.go
go test -cover ./internal/worker/iota_payment_worker_test.go
```

### Run Specific Test

```bash
go test -v ./internal/repository -run TestIOTARepository_CreateWallet
```

## Current State: RED Phase ✗

All tests are designed to **FAIL** currently because:

- ✗ No repository implementation exists
- ✗ No IOTA client implementation exists
- ✗ No payment service implementation exists
- ✗ No HTTP handlers implementation exists
- ✗ No worker implementation exists
- ✗ No database migrations created

## Next Steps (Part 2: Implementation)

1. **Create Database Migration** (Sprint 2 - Epic 1, Part 2)
   - Migration file: `042_add_iota_payments.sql`
   - Create tables, indexes, constraints

2. **Implement Repository** (`internal/repository/iota_repository.go`)
   - CRUD operations for wallets, intents, transactions
   - Query methods for active/expired intents

3. **Implement IOTA Client** (`internal/payments/iota_client.go`)
   - Seed generation with crypto/rand
   - Address derivation (BIP32/BIP44 compatible)
   - Node communication (balance, transaction, status)

4. **Implement Payment Service** (`internal/usecase/payments/payment_service.go`)
   - Wallet management
   - Payment intent creation
   - Payment detection logic
   - Seed encryption/decryption (AES-256-GCM)

5. **Implement HTTP Handlers** (`internal/httpapi/handlers/payments/payment_handlers.go`)
   - Wallet endpoints
   - Payment intent endpoints
   - Transaction history endpoint

6. **Implement Worker** (`internal/worker/iota_payment_worker.go`)
   - Background polling
   - Payment detection
   - Confirmation tracking
   - Intent expiration

7. **Run Tests and Iterate** (GREEN Phase)
   - Run all tests
   - Fix implementation issues
   - Achieve test passage
   - Refactor as needed (REFACTOR phase)

## Test Quality Metrics

- ✓ **Comprehensive Coverage:** 100+ test cases across full stack
- ✓ **Table-Driven Tests:** Used extensively for edge cases
- ✓ **Mocking:** All external dependencies mocked (no network calls)
- ✓ **Security Focus:** Encryption, seed protection, input validation
- ✓ **Edge Cases:** Network errors, timeouts, invalid data, constraints
- ✓ **Concurrency:** Worker lifecycle and context handling
- ✓ **Integration Ready:** Tests use real database (testutil.SetupTestDB)

## Files Created

1. `/home/user/vidra/internal/domain/payment.go` - Domain models
2. `/home/user/vidra/internal/repository/iota_repository_test.go` - Repository tests
3. `/home/user/vidra/internal/payments/iota_client_test.go` - IOTA client tests
4. `/home/user/vidra/internal/usecase/payments/payment_service_test.go` - Service tests
5. `/home/user/vidra/internal/httpapi/handlers/payments/payment_handlers_test.go` - Handler tests
6. `/home/user/vidra/internal/worker/iota_payment_worker_test.go` - Worker tests

**Total Lines of Test Code:** ~3,000+ lines

---

**Status:** ✓ TDD Tests Complete (RED Phase)
**Next:** Implementation (Part 2) to achieve GREEN Phase
