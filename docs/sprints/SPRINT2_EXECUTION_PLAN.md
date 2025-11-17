# Sprint 2 Execution Plan
**Sprint Duration:** 22-28 hours (3-4 days with 7 agents)
**Sprint Goal:** Implement IOTA payments, complete ActivityPub video federation, establish observability, and configure Go-Atlas
**Sprint Priority:** **CRITICAL** - Addresses core feature gaps and operational visibility

---

## Sprint Objectives

1. **IOTA Payments** (8-12 hours) - Enable creator monetization and decentralized transactions
2. **ActivityPub Video Federation** (6-8 hours) - Restore PeerTube compatibility for video sharing
3. **Observability** (6-8 hours) - Structured logging, metrics expansion, OpenTelemetry tracing
4. **Go-Atlas Configuration** (2-3 hours) - Professional migration management

**Success Criteria:**
- ✅ IOTA wallet creation, payment processing, and transaction verification working
- ✅ Videos federate to Mastodon/PeerTube on upload completion
- ✅ Structured logging across all modules with request IDs
- ✅ Distributed tracing end-to-end for video upload → processing → federation
- ✅ Go-Atlas managing migrations with lint checks and shadow DB validation
- ✅ Test coverage: 80%+ for all new code
- ✅ All builds passing in CI

---

## Agent Assignments

### Available Agents (7)

1. **Go Backend Reviewer** - Code quality, architecture, best practices
2. **Golang Test Guardian** - Test coverage, TDD enforcement, test quality
3. **Federation Protocol Auditor** - ActivityPub compliance, protocol correctness
4. **Decentralized Systems Security Expert** - IOTA security, crypto validation
5. **API Edge Tester** - HTTP handlers, API contracts, integration tests
6. **Infra Solutions Engineer** - Observability, deployment, Go-Atlas configuration
7. **Decentralized Video PM** - Coordination, priorities, acceptance criteria

---

## Epic 1: IOTA Payments Implementation

**Priority:** CRITICAL
**Effort:** 8-12 hours
**Owner:** Decentralized Systems Security Expert (lead), Go Backend Reviewer (review)

### Objective

Implement IOTA payment integration for creator monetization, video purchases, and decentralized transactions. Support IOTA Tangle integration with wallet management, payment verification, and transaction history.

### Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                      Payment Flow                            │
├─────────────────────────────────────────────────────────────┤
│ 1. User initiates payment (video purchase, donation, tip)   │
│ 2. Service generates unique IOTA address for transaction    │
│ 3. User sends IOTA tokens to generated address              │
│ 4. Background worker polls IOTA node for confirmations      │
│ 5. On confirmation, grant access / credit creator wallet    │
│ 6. Store transaction history for audit / reconciliation     │
└─────────────────────────────────────────────────────────────┘
```

### Tasks

#### Task 1.1: Database Schema (1 hour)
**Assignee:** Decentralized Systems Security Expert
**Deliverable:** Migration file + domain models

**Files to Create:**
- `/home/user/athena/migrations/058_create_iota_payments_tables.sql`

**Schema:**
```sql
-- User wallets (one per user, HD derivation path)
CREATE TABLE iota_wallets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    seed_encrypted TEXT NOT NULL,  -- AES-256 encrypted seed
    address_index INT DEFAULT 0,   -- HD derivation counter
    balance_iota BIGINT DEFAULT 0, -- Cache of confirmed balance (base units)
    last_sync_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(user_id)
);

CREATE INDEX idx_iota_wallets_user_id ON iota_wallets(user_id);

-- Payment transactions
CREATE TABLE iota_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id TEXT UNIQUE NOT NULL, -- IOTA transaction ID
    from_address TEXT,
    to_address TEXT NOT NULL,
    amount_iota BIGINT NOT NULL,
    metadata JSONB,  -- {videoId, userId, purchaseType, message}
    status TEXT NOT NULL CHECK (status IN ('pending', 'confirmed', 'failed')),
    confirmations INT DEFAULT 0,
    block_id TEXT,   -- IOTA block ID
    user_id UUID REFERENCES users(id),
    video_id UUID REFERENCES videos(id),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    confirmed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_iota_transactions_status ON iota_transactions(status);
CREATE INDEX idx_iota_transactions_user_id ON iota_transactions(user_id);
CREATE INDEX idx_iota_transactions_video_id ON iota_transactions(video_id);
CREATE INDEX idx_iota_transactions_to_address ON iota_transactions(to_address);

-- Payment intents (pre-transaction state)
CREATE TABLE iota_payment_intents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    video_id UUID REFERENCES videos(id),
    amount_iota BIGINT NOT NULL,
    payment_address TEXT NOT NULL,
    purpose TEXT NOT NULL, -- 'purchase', 'donation', 'tip'
    metadata JSONB,
    status TEXT NOT NULL CHECK (status IN ('pending', 'completed', 'expired', 'cancelled')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_iota_payment_intents_status ON iota_payment_intents(status);
CREATE INDEX idx_iota_payment_intents_user_id ON iota_payment_intents(user_id);
CREATE INDEX idx_iota_payment_intents_payment_address ON iota_payment_intents(payment_address);
```

**Domain Models:** `/home/user/athena/internal/domain/iota.go`
```go
package domain

import "time"

// IOTAWallet represents a user's IOTA wallet
type IOTAWallet struct {
    ID            string     `json:"id" db:"id"`
    UserID        string     `json:"user_id" db:"user_id"`
    SeedEncrypted string     `json:"-" db:"seed_encrypted"` // Never expose in JSON
    AddressIndex  int        `json:"address_index" db:"address_index"`
    BalanceIOTA   int64      `json:"balance_iota" db:"balance_iota"`
    LastSyncAt    *time.Time `json:"last_sync_at" db:"last_sync_at"`
    CreatedAt     time.Time  `json:"created_at" db:"created_at"`
    UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
}

// IOTATransaction represents an IOTA payment transaction
type IOTATransaction struct {
    ID            string     `json:"id" db:"id"`
    TransactionID string     `json:"transaction_id" db:"transaction_id"`
    FromAddress   *string    `json:"from_address" db:"from_address"`
    ToAddress     string     `json:"to_address" db:"to_address"`
    AmountIOTA    int64      `json:"amount_iota" db:"amount_iota"`
    Metadata      []byte     `json:"metadata" db:"metadata"` // JSONB
    Status        string     `json:"status" db:"status"`
    Confirmations int        `json:"confirmations" db:"confirmations"`
    BlockID       *string    `json:"block_id" db:"block_id"`
    UserID        *string    `json:"user_id" db:"user_id"`
    VideoID       *string    `json:"video_id" db:"video_id"`
    CreatedAt     time.Time  `json:"created_at" db:"created_at"`
    ConfirmedAt   *time.Time `json:"confirmed_at" db:"confirmed_at"`
    UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
}

// IOTAPaymentIntent represents a payment request before transaction creation
type IOTAPaymentIntent struct {
    ID             string    `json:"id" db:"id"`
    UserID         string    `json:"user_id" db:"user_id"`
    VideoID        *string   `json:"video_id" db:"video_id"`
    AmountIOTA     int64     `json:"amount_iota" db:"amount_iota"`
    PaymentAddress string    `json:"payment_address" db:"payment_address"`
    Purpose        string    `json:"purpose" db:"purpose"`
    Metadata       []byte    `json:"metadata" db:"metadata"` // JSONB
    Status         string    `json:"status" db:"status"`
    ExpiresAt      time.Time `json:"expires_at" db:"expires_at"`
    CreatedAt      time.Time `json:"created_at" db:"created_at"`
    UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}
```

**Acceptance Criteria:**
- ✅ Migration applies cleanly with `atlas migrate apply`
- ✅ All indexes created for query optimization
- ✅ Foreign keys cascade properly
- ✅ Domain models match DB schema 1:1

---

#### Task 1.2: IOTA Client & Wallet Service (4-5 hours)
**Assignee:** Decentralized Systems Security Expert
**Reviewers:** Go Backend Reviewer (architecture), Golang Test Guardian (tests)

**Files to Create:**

1. `/home/user/athena/internal/iota/client.go` - IOTA node client
2. `/home/user/athena/internal/iota/wallet.go` - Wallet management (seed generation, address derivation)
3. `/home/user/athena/internal/iota/transaction.go` - Transaction building and submission
4. `/home/user/athena/internal/crypto/aes.go` - AES-256-GCM encryption for seed storage
5. `/home/user/athena/internal/repository/iota_repository.go` - Database persistence
6. `/home/user/athena/internal/usecase/payments/iota_service.go` - Business logic

**IOTA Client:** `/home/user/athena/internal/iota/client.go`
```go
package iota

import (
    "context"
    "time"

    // Use official IOTA Go library
    iotago "github.com/iotaledger/iota.go/v3"
    "github.com/iotaledger/iota.go/v3/nodeclient"
)

type Client struct {
    nodeAPI *nodeclient.Client
    timeout time.Duration
}

func NewClient(nodeURL string, timeout time.Duration) (*Client, error) {
    api := nodeclient.New(nodeURL)
    return &Client{
        nodeAPI: api,
        timeout: timeout,
    }, nil
}

// GetBalance retrieves balance for an address
func (c *Client) GetBalance(ctx context.Context, address string) (uint64, error) {
    // Implementation using IOTA API
}

// SubmitBlock submits a transaction block to the tangle
func (c *Client) SubmitBlock(ctx context.Context, block *iotago.Block) (string, error) {
    // Implementation
}

// GetBlock retrieves a block by ID
func (c *Client) GetBlock(ctx context.Context, blockID string) (*iotago.Block, error) {
    // Implementation
}

// GetOutputsByAddress retrieves UTXOs for an address
func (c *Client) GetOutputsByAddress(ctx context.Context, address string) ([]iotago.Output, error) {
    // Implementation
}
```

**Wallet Service:** `/home/user/athena/internal/iota/wallet.go`
```go
package iota

import (
    "crypto/rand"
    "encoding/hex"

    iotago "github.com/iotaledger/iota.go/v3"
    "github.com/iotaledger/iota.go/v3/ed25519"
)

// GenerateSeed generates a secure random seed (64 bytes)
func GenerateSeed() (string, error) {
    seed := make([]byte, 64)
    if _, err := rand.Read(seed); err != nil {
        return "", err
    }
    return hex.EncodeToString(seed), nil
}

// DeriveAddress derives an address from seed and index (BIP32-like)
func DeriveAddress(seed string, index uint32) (string, error) {
    // Use IOTA address derivation
    seedBytes, _ := hex.DecodeString(seed)
    keyPair := ed25519.GenerateKey(seedBytes, index)
    address := iotago.AddressFromEd25519PubKey(keyPair.PublicKey)
    return address.Bech32(iotago.PrefixMainnet), nil
}

// BuildTransaction creates a transaction from inputs to outputs
func BuildTransaction(inputs []iotago.Output, outputs []iotago.Output, privateKey ed25519.PrivateKey) (*iotago.Block, error) {
    // Implementation
}
```

**Encryption:** `/home/user/athena/internal/crypto/aes.go`
```go
package crypto

import (
    "crypto/aes"
    "crypto/cipher"
    "crypto/rand"
    "encoding/base64"
    "io"
)

// EncryptAES256GCM encrypts plaintext with AES-256-GCM
func EncryptAES256GCM(plaintext, key []byte) (string, error) {
    block, err := aes.NewCipher(key) // key must be 32 bytes
    if err != nil {
        return "", err
    }

    gcm, err := cipher.NewGCM(block)
    if err != nil {
        return "", err
    }

    nonce := make([]byte, gcm.NonceSize())
    if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
        return "", err
    }

    ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
    return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptAES256GCM decrypts ciphertext with AES-256-GCM
func DecryptAES256GCM(ciphertext string, key []byte) ([]byte, error) {
    data, err := base64.StdEncoding.DecodeString(ciphertext)
    if err != nil {
        return nil, err
    }

    block, err := aes.NewCipher(key)
    if err != nil {
        return nil, err
    }

    gcm, err := cipher.NewGCM(block)
    if err != nil {
        return nil, err
    }

    nonceSize := gcm.NonceSize()
    nonce, ciphertext := data[:nonceSize], data[nonceSize:]

    plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
    if err != nil {
        return nil, err
    }

    return plaintext, nil
}
```

**Payment Service:** `/home/user/athena/internal/usecase/payments/iota_service.go`
```go
package payments

import (
    "context"
    "encoding/json"
    "time"

    "athena/internal/domain"
    "athena/internal/iota"
)

type IOTAService struct {
    iotaClient *iota.Client
    repo       IOTARepository
    encKey     []byte // 32-byte AES key from env
}

// CreateWallet creates a new IOTA wallet for a user
func (s *IOTAService) CreateWallet(ctx context.Context, userID string) (*domain.IOTAWallet, error) {
    // 1. Generate seed
    // 2. Encrypt seed with AES-256-GCM
    // 3. Store in database
    // 4. Return wallet
}

// CreatePaymentIntent creates a payment request
func (s *IOTAService) CreatePaymentIntent(ctx context.Context, userID, videoID string, amountIOTA int64, purpose string) (*domain.IOTAPaymentIntent, error) {
    // 1. Get user wallet
    // 2. Derive new address (increment index)
    // 3. Create payment intent (expires in 1 hour)
    // 4. Return address for user to send payment
}

// CheckPayment checks if a payment has been received and confirmed
func (s *IOTAService) CheckPayment(ctx context.Context, paymentIntentID string) (*domain.IOTATransaction, error) {
    // 1. Get payment intent
    // 2. Query IOTA node for address balance
    // 3. If payment received, create transaction record
    // 4. Update intent status to 'completed'
    // 5. Grant access to video (if purchase)
}

// SyncWallet syncs wallet balance with IOTA tangle
func (s *IOTAService) SyncWallet(ctx context.Context, walletID string) error {
    // 1. Get all derived addresses
    // 2. Query balances from IOTA node
    // 3. Sum balances
    // 4. Update wallet.balance_iota
}
```

**Acceptance Criteria:**
- ✅ Wallet seed generated with crypto/rand (secure randomness)
- ✅ Seed encrypted with AES-256-GCM before database storage
- ✅ Address derivation follows IOTA standards (Bech32 encoding)
- ✅ Transaction building supports metadata embedding (JSON in tagged data)
- ✅ All errors wrapped with context (fmt.Errorf)
- ✅ Test coverage: 80%+ (unit tests with mocked IOTA client)

**Security Requirements:**
- ✅ Seed NEVER logged or exposed in API responses
- ✅ AES key loaded from environment (IOTA_ENCRYPTION_KEY), never hardcoded
- ✅ Rate limit wallet creation (max 1 per user)
- ✅ Validate IOTA addresses (checksum, prefix)

---

#### Task 1.3: Background Payment Worker (2-3 hours)
**Assignee:** Decentralized Systems Security Expert
**Reviewers:** Infra Solutions Engineer (worker patterns)

**Files to Create:**
- `/home/user/athena/internal/worker/iota_payment_worker.go`

**Implementation:**
```go
package worker

import (
    "context"
    "log"
    "time"

    "athena/internal/usecase/payments"
)

type IOTAPaymentWorker struct {
    service      *payments.IOTAService
    pollInterval time.Duration
    done         chan struct{}
}

func NewIOTAPaymentWorker(service *payments.IOTAService, pollInterval time.Duration) *IOTAPaymentWorker {
    return &IOTAPaymentWorker{
        service:      service,
        pollInterval: pollInterval,
        done:         make(chan struct{}),
    }
}

// Start begins polling for pending payment intents
func (w *IOTAPaymentWorker) Start(ctx context.Context) {
    ticker := time.NewTicker(w.pollInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-w.done:
            return
        case <-ticker.C:
            w.processPendingPayments(ctx)
        }
    }
}

// processPendingPayments checks all pending payment intents
func (w *IOTAPaymentWorker) processPendingPayments(ctx context.Context) {
    // 1. Query pending payment intents (status='pending', expires_at > NOW())
    // 2. For each intent, call service.CheckPayment()
    // 3. If confirmed, grant access to video
    // 4. If expired, update status to 'expired'
    // 5. Log metrics (confirmations, failures)
}

func (w *IOTAPaymentWorker) Stop() {
    close(w.done)
}
```

**Acceptance Criteria:**
- ✅ Worker polls every 30 seconds (configurable via env)
- ✅ Graceful shutdown on context cancellation
- ✅ Expired intents marked as 'expired' after 1 hour
- ✅ Confirmed payments trigger access grant (video purchase)
- ✅ Prometheus metrics: `iota_payments_confirmed_total`, `iota_payments_pending_count`

---

#### Task 1.4: HTTP API Handlers (1-2 hours)
**Assignee:** API Edge Tester
**Reviewers:** Go Backend Reviewer

**Files to Create:**
- `/home/user/athena/internal/httpapi/handlers/payments/iota_handlers.go`
- `/home/user/athena/internal/httpapi/handlers/payments/iota_handlers_test.go`

**Endpoints:**

1. **POST /api/v1/payments/wallet** - Create wallet for user
2. **GET /api/v1/payments/wallet** - Get user's wallet balance
3. **POST /api/v1/payments/intent** - Create payment intent (purchase video, donate, tip)
4. **GET /api/v1/payments/intent/:id** - Get payment intent status
5. **GET /api/v1/payments/transactions** - List user's transaction history

**Example Handler:**
```go
// POST /api/v1/payments/intent
func (h *PaymentHandler) CreatePaymentIntent(w http.ResponseWriter, r *http.Request) {
    var req struct {
        VideoID    string `json:"video_id" validate:"required,uuid"`
        AmountIOTA int64  `json:"amount_iota" validate:"required,min=1"`
        Purpose    string `json:"purpose" validate:"required,oneof=purchase donation tip"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        RespondError(w, http.StatusBadRequest, "invalid request body")
        return
    }

    userID := GetUserIDFromContext(r.Context())
    intent, err := h.service.CreatePaymentIntent(r.Context(), userID, req.VideoID, req.AmountIOTA, req.Purpose)
    if err != nil {
        RespondError(w, http.StatusInternalServerError, err.Error())
        return
    }

    RespondJSON(w, http.StatusCreated, intent)
}
```

**Acceptance Criteria:**
- ✅ All endpoints require authentication (JWT middleware)
- ✅ Input validation with go-playground/validator
- ✅ Consistent error responses (JSON problem details)
- ✅ Test coverage: 85%+ (integration tests with test database)

---

#### Task 1.5: Integration Tests (1-2 hours)
**Assignee:** Golang Test Guardian

**Files to Create:**
- `/home/user/athena/internal/usecase/payments/iota_service_test.go`
- `/home/user/athena/internal/iota/client_test.go`
- `/home/user/athena/internal/httpapi/handlers/payments/iota_integration_test.go`

**Test Scenarios:**
1. ✅ Wallet creation generates unique seed per user
2. ✅ Address derivation is deterministic (same seed+index → same address)
3. ✅ Payment intent creation increments address index
4. ✅ Payment worker detects confirmed transactions
5. ✅ Expired intents do not grant access
6. ✅ Duplicate payment intents rejected
7. ✅ Seed encryption/decryption roundtrip

**Acceptance Criteria:**
- ✅ All tests pass without external IOTA node (use mock client)
- ✅ Test coverage: 80%+
- ✅ No flaky tests (deterministic seed generation in tests)

---

### Epic 1 Summary

**Total Effort:** 8-12 hours
**Files Created:** 11 files
**Tests Added:** ~45 tests
**Metrics:**
- `iota_wallets_created_total`
- `iota_payments_confirmed_total`
- `iota_payments_pending_count`
- `iota_payment_worker_poll_duration_seconds`

**Dependencies Added:**
```bash
go get github.com/iotaledger/iota.go/v3
go get github.com/iotaledger/iota.go/v3/nodeclient
```

---

## Epic 2: ActivityPub Video Federation

**Priority:** CRITICAL
**Effort:** 6-8 hours
**Owner:** Federation Protocol Auditor (lead), Go Backend Reviewer (review)

### Objective

Complete ActivityPub video federation by implementing VideoObject creation and Create activity delivery when videos are uploaded and processed. Ensure compatibility with Mastodon, PeerTube, and other ActivityPub platforms.

### Current Status

- ✅ VideoObject domain model defined (`/home/user/athena/internal/domain/activitypub.go`)
- ✅ ActivityPub service handles Follow/Like/Announce
- ✅ HTTP signature verification and delivery working
- ❌ No method to build VideoObject from domain.Video
- ❌ No Create activity triggered on video upload completion
- ❌ Comments not federated

### Tasks

#### Task 2.1: VideoObject Builder (2 hours)
**Assignee:** Federation Protocol Auditor

**Files to Modify:**
- `/home/user/athena/internal/usecase/activitypub/service.go` (add methods)
- `/home/user/athena/internal/usecase/activitypub/video.go` (new file)

**Implementation:** `/home/user/athena/internal/usecase/activitypub/video.go`
```go
package activitypub

import (
    "context"
    "fmt"
    "time"

    "athena/internal/domain"
)

// BuildVideoObject converts a domain.Video to an ActivityPub VideoObject
func (s *Service) BuildVideoObject(ctx context.Context, video *domain.Video) (*domain.VideoObject, error) {
    // Get video owner
    owner, err := s.userRepo.GetByID(ctx, video.UserID)
    if err != nil {
        return nil, fmt.Errorf("failed to get video owner: %w", err)
    }

    actorID := s.buildActorID(owner.Username)
    videoURI := fmt.Sprintf("%s/videos/%s", s.cfg.PublicBaseURL, video.ID)

    // Build URL array with streaming variants
    urls := []domain.APUrl{
        {
            Type:      "Link",
            MediaType: "video/mp4",
            Href:      fmt.Sprintf("%s/videos/%s/master.m3u8", s.cfg.PublicBaseURL, video.ID),
        },
    }

    // Add HLS variants
    for _, variant := range video.Outputs {
        urls = append(urls, domain.APUrl{
            Type:      "Link",
            MediaType: "application/x-mpegURL",
            Href:      variant.URL,
            Height:    variant.Height,
            Width:     variant.Width,
        })
    }

    // Build thumbnail icons
    icons := []domain.Image{}
    if video.ThumbnailURL != "" {
        icons = append(icons, domain.Image{
            Type:      "Image",
            MediaType: "image/jpeg",
            URL:       video.ThumbnailURL,
        })
    }

    // Build tags
    tags := []domain.APTag{}
    for _, tag := range video.Tags {
        tags = append(tags, domain.APTag{
            Type: "Hashtag",
            Name: "#" + tag,
            Href: fmt.Sprintf("%s/tags/%s", s.cfg.PublicBaseURL, tag),
        })
    }

    videoObject := &domain.VideoObject{
        Context:         []interface{}{domain.ActivityStreamsContext, domain.PeerTubeContext},
        Type:            domain.ObjectTypeVideo,
        ID:              videoURI,
        Name:            video.Title,
        Duration:        formatISO8601Duration(video.Duration),
        UUID:            video.ID,
        Views:           int(video.ViewCount),
        Sensitive:       video.Privacy == "private",
        WaitTranscoding: video.ProcessingStatus == "processing",
        CommentsEnabled: true, // TODO: Add to video model
        DownloadEnabled: true,
        Published:       &video.UploadDate,
        Updated:         &video.UpdatedAt,
        MediaType:       "text/markdown",
        Content:         video.Description,
        Summary:         truncate(video.Description, 200),
        Icon:            icons,
        URL:             urls,
        Likes:           videoURI + "/likes",
        Dislikes:        videoURI + "/dislikes",
        Shares:          videoURI + "/shares",
        Comments:        videoURI + "/comments",
        AttributedTo:    []string{actorID},
        To:              []string{"https://www.w3.org/ns/activitystreams#Public"},
        Cc:              []string{actorID + "/followers"},
        Tag:             tags,
    }

    return videoObject, nil
}

// formatISO8601Duration converts seconds to ISO 8601 duration (e.g., PT1H30M)
func formatISO8601Duration(seconds int) string {
    hours := seconds / 3600
    minutes := (seconds % 3600) / 60
    secs := seconds % 60

    if hours > 0 {
        return fmt.Sprintf("PT%dH%dM%dS", hours, minutes, secs)
    } else if minutes > 0 {
        return fmt.Sprintf("PT%dM%dS", minutes, secs)
    }
    return fmt.Sprintf("PT%dS", secs)
}

func truncate(s string, maxLen int) string {
    if len(s) <= maxLen {
        return s
    }
    return s[:maxLen] + "..."
}
```

**Acceptance Criteria:**
- ✅ VideoObject includes all required ActivityPub fields
- ✅ PeerTube context added for compatibility
- ✅ HLS variants mapped to APUrl array
- ✅ Tags converted to Hashtag format
- ✅ Thumbnails mapped to Icon array
- ✅ ISO 8601 duration formatting (PT1H30M format)
- ✅ Public videos have To: ["Public"], Cc: [followers]
- ✅ Private videos omit Public addressing

---

#### Task 2.2: Create Activity Delivery (2-3 hours)
**Assignee:** Federation Protocol Auditor
**Reviewers:** Go Backend Reviewer

**Files to Modify:**
- `/home/user/athena/internal/usecase/activitypub/service.go` (add PublishVideo method)
- `/home/user/athena/internal/usecase/encoding/service.go` (integrate PublishVideo call)

**Implementation:** Add to `/home/user/athena/internal/usecase/activitypub/service.go`
```go
// PublishVideo creates and delivers a Create activity for a video
func (s *Service) PublishVideo(ctx context.Context, videoID string) error {
    // 1. Get video
    video, err := s.videoRepo.GetByID(ctx, videoID)
    if err != nil {
        return fmt.Errorf("failed to get video: %w", err)
    }

    // Skip if not public
    if video.Privacy != "public" {
        return nil
    }

    // 2. Build VideoObject
    videoObject, err := s.BuildVideoObject(ctx, video)
    if err != nil {
        return fmt.Errorf("failed to build video object: %w", err)
    }

    // 3. Get video owner
    owner, err := s.userRepo.GetByID(ctx, video.UserID)
    if err != nil {
        return fmt.Errorf("failed to get owner: %w", err)
    }

    actorID := s.buildActorID(owner.Username)
    activityID := fmt.Sprintf("%s#create-%s", actorID, video.ID)

    // 4. Build Create activity
    createActivity := map[string]interface{}{
        "@context":  []interface{}{domain.ActivityStreamsContext, domain.PeerTubeContext},
        "type":      domain.ActivityTypeCreate,
        "id":        activityID,
        "actor":     actorID,
        "object":    videoObject,
        "published": video.UploadDate.Format(time.RFC3339),
        "to":        []string{"https://www.w3.org/ns/activitystreams#Public"},
        "cc":        []string{actorID + "/followers"},
    }

    // 5. Store activity locally
    activityJSON, _ := json.Marshal(createActivity)
    apActivity := &domain.APActivity{
        ActorID:      owner.ID,
        Type:         domain.ActivityTypeCreate,
        ObjectID:     &video.ID,
        ObjectType:   &domain.ObjectTypeVideo,
        Published:    video.UploadDate,
        ActivityJSON: activityJSON,
        Local:        true,
    }

    if err := s.repo.StoreActivity(ctx, apActivity); err != nil {
        return fmt.Errorf("failed to store activity: %w", err)
    }

    // 6. Get followers
    followers, _, err := s.repo.GetFollowers(ctx, owner.ID, "accepted", 10000, 0)
    if err != nil {
        return fmt.Errorf("failed to get followers: %w", err)
    }

    // 7. Enqueue delivery to each follower's inbox
    for _, follower := range followers {
        // Get remote actor for inbox URL
        remoteActor, err := s.repo.GetRemoteActor(ctx, follower.FollowerID)
        if err != nil {
            continue // Skip if actor not cached
        }

        inboxURL := remoteActor.InboxURL
        if remoteActor.SharedInbox != nil && *remoteActor.SharedInbox != "" {
            inboxURL = *remoteActor.SharedInbox // Prefer shared inbox
        }

        // Enqueue delivery
        if err := s.enqueueDelivery(ctx, apActivity.ID, inboxURL, owner.ID); err != nil {
            log.Printf("failed to enqueue delivery to %s: %v", inboxURL, err)
        }
    }

    return nil
}

func (s *Service) enqueueDelivery(ctx context.Context, activityID, inboxURL, actorID string) error {
    delivery := &domain.APDeliveryQueue{
        ActivityID:  activityID,
        InboxURL:    inboxURL,
        ActorID:     actorID,
        Attempts:    0,
        MaxAttempts: 10,
        NextAttempt: time.Now(),
        Status:      "pending",
    }
    return s.repo.EnqueueDelivery(ctx, delivery)
}
```

**Integration:** Modify `/home/user/athena/internal/usecase/encoding/service.go`
```go
// After video processing completes successfully
if video.ProcessingStatus == "completed" {
    // Existing ATProto publish
    if err := s.atproto.PublishVideo(ctx, v); err != nil && s.fedEnq != nil {
        _ = s.enqueuePublishRetry(ctx, v.ID, 30*time.Second)
    }

    // NEW: ActivityPub publish
    if s.activityPubService != nil {
        if err := s.activityPubService.PublishVideo(ctx, v.ID); err != nil {
            log.Printf("Failed to publish video to ActivityPub: %v", err)
            // Non-fatal error, continue
        }
    }
}
```

**Acceptance Criteria:**
- ✅ Create activity generated on video processing completion
- ✅ Only public videos federated (privacy='public')
- ✅ Activity stored in `ap_activities` table
- ✅ Delivery enqueued for all accepted followers
- ✅ Shared inbox preferred over individual inboxes (reduces deliveries)
- ✅ Non-blocking: federation failure does not fail video upload
- ✅ Retry logic via delivery worker (existing)

---

#### Task 2.3: Comment Federation (1-2 hours)
**Assignee:** Federation Protocol Auditor

**Files to Create:**
- `/home/user/athena/internal/usecase/activitypub/comment.go`

**Files to Modify:**
- `/home/user/athena/internal/usecase/comment/service.go` (trigger federation on comment creation)

**Implementation:**
```go
package activitypub

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "athena/internal/domain"
)

// PublishComment federates a new comment as a Create activity
func (s *Service) PublishComment(ctx context.Context, commentID string) error {
    // 1. Get comment
    comment, err := s.commentRepo.GetByID(ctx, commentID)
    if err != nil {
        return fmt.Errorf("failed to get comment: %w", err)
    }

    // 2. Get video
    video, err := s.videoRepo.GetByID(ctx, comment.VideoID)
    if err != nil {
        return fmt.Errorf("failed to get video: %w", err)
    }

    // Skip if video is not public
    if video.Privacy != "public" {
        return nil
    }

    // 3. Get comment author
    author, err := s.userRepo.GetByID(ctx, comment.UserID)
    if err != nil {
        return fmt.Errorf("failed to get author: %w", err)
    }

    actorID := s.buildActorID(author.Username)
    commentURI := fmt.Sprintf("%s/videos/%s/comments/%s", s.cfg.PublicBaseURL, video.ID, comment.ID)
    videoURI := fmt.Sprintf("%s/videos/%s", s.cfg.PublicBaseURL, video.ID)

    // 4. Build Note object (comments are Notes in ActivityPub)
    noteObject := map[string]interface{}{
        "type":        "Note",
        "id":          commentURI,
        "content":     comment.Text,
        "mediaType":   "text/plain",
        "inReplyTo":   videoURI,
        "published":   comment.CreatedAt.Format(time.RFC3339),
        "attributedTo": actorID,
        "to":          []string{"https://www.w3.org/ns/activitystreams#Public"},
        "cc":          []string{actorID + "/followers"},
    }

    // 5. Build Create activity
    activityID := fmt.Sprintf("%s#create-comment-%s", actorID, comment.ID)
    createActivity := map[string]interface{}{
        "@context":  domain.ActivityStreamsContext,
        "type":      domain.ActivityTypeCreate,
        "id":        activityID,
        "actor":     actorID,
        "object":    noteObject,
        "published": comment.CreatedAt.Format(time.RFC3339),
        "to":        []string{"https://www.w3.org/ns/activitystreams#Public"},
        "cc":        []string{actorID + "/followers"},
    }

    // 6. Store and deliver (same as PublishVideo)
    // ... (similar logic to PublishVideo)

    return nil
}
```

**Acceptance Criteria:**
- ✅ Comments federated as Note objects
- ✅ inReplyTo points to video URI
- ✅ Delivered to author's followers
- ✅ Public comments only

---

#### Task 2.4: Federation Integration Tests (1-2 hours)
**Assignee:** Golang Test Guardian

**Files to Create:**
- `/home/user/athena/internal/usecase/activitypub/video_test.go`
- `/home/user/athena/internal/usecase/activitypub/comment_test.go`

**Test Scenarios:**
1. ✅ BuildVideoObject includes all required fields
2. ✅ PublishVideo creates activity and enqueues deliveries
3. ✅ Private videos not federated
4. ✅ Videos with no followers skip delivery
5. ✅ Shared inbox used when available
6. ✅ Comments federated as Note objects
7. ✅ Comment inReplyTo points to correct video

**Acceptance Criteria:**
- ✅ Test coverage: 85%+
- ✅ No external network calls (mock HTTP client)
- ✅ All tests pass in CI

---

### Epic 2 Summary

**Total Effort:** 6-8 hours
**Files Created/Modified:** 7 files
**Tests Added:** ~30 tests
**Federation Coverage:**
- ✅ Videos federate on upload completion
- ✅ Comments federate on creation
- ✅ Public videos visible on Mastodon/PeerTube
- ✅ Likes, Shares, Follows already working (Sprint 1)

---

## Epic 3: Observability (Structured Logging, Metrics, Tracing)

**Priority:** HIGH
**Effort:** 6-8 hours
**Owner:** Infra Solutions Engineer (lead), Go Backend Reviewer (review)

### Objective

Implement production-grade observability with structured logging (slog), expanded Prometheus metrics, and OpenTelemetry distributed tracing for end-to-end request tracking.

### Current Status

- ✅ Basic Prometheus metrics (encoder, federation)
- ⚠️ OpenTelemetry dependency present but not configured
- ❌ No structured logging (mix of log.Printf and logrus)
- ❌ No distributed tracing

### Tasks

#### Task 3.1: Structured Logging with slog (2-3 hours)
**Assignee:** Infra Solutions Engineer

**Files to Create:**
- `/home/user/athena/internal/obs/logger.go` - Centralized logger factory
- `/home/user/athena/internal/obs/context.go` - Request-scoped logging

**Implementation:** `/home/user/athena/internal/obs/logger.go`
```go
package obs

import (
    "context"
    "log/slog"
    "os"
)

var defaultLogger *slog.Logger

// InitLogger initializes the global structured logger
func InitLogger(level string, format string) {
    var logLevel slog.Level
    switch level {
    case "debug":
        logLevel = slog.LevelDebug
    case "info":
        logLevel = slog.LevelInfo
    case "warn":
        logLevel = slog.LevelWarn
    case "error":
        logLevel = slog.LevelError
    default:
        logLevel = slog.LevelInfo
    }

    opts := &slog.HandlerOptions{
        Level: logLevel,
    }

    var handler slog.Handler
    if format == "json" {
        handler = slog.NewJSONHandler(os.Stdout, opts)
    } else {
        handler = slog.NewTextHandler(os.Stdout, opts)
    }

    defaultLogger = slog.New(handler)
    slog.SetDefault(defaultLogger)
}

// Logger returns the default logger
func Logger() *slog.Logger {
    if defaultLogger == nil {
        InitLogger("info", "text")
    }
    return defaultLogger
}

// WithRequestID returns a logger with request_id field
func WithRequestID(ctx context.Context, requestID string) *slog.Logger {
    return Logger().With("request_id", requestID)
}

// WithUserID returns a logger with user_id field
func WithUserID(ctx context.Context, userID string) *slog.Logger {
    return Logger().With("user_id", userID)
}

// WithFields returns a logger with arbitrary fields
func WithFields(fields ...any) *slog.Logger {
    return Logger().With(fields...)
}
```

**Context Integration:** `/home/user/athena/internal/obs/context.go`
```go
package obs

import (
    "context"
    "log/slog"
)

type contextKey string

const loggerKey contextKey = "logger"

// ContextWithLogger stores a logger in context
func ContextWithLogger(ctx context.Context, logger *slog.Logger) context.Context {
    return context.WithValue(ctx, loggerKey, logger)
}

// LoggerFromContext retrieves logger from context, or returns default
func LoggerFromContext(ctx context.Context) *slog.Logger {
    if logger, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
        return logger
    }
    return Logger()
}
```

**Middleware Integration:** Add to `/home/user/athena/internal/middleware/logging.go`
```go
package middleware

import (
    "net/http"
    "time"

    "athena/internal/obs"
    "github.com/go-chi/chi/v5/middleware"
)

func RequestLogger(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        requestID := middleware.GetReqID(r.Context())
        logger := obs.WithFields(
            "request_id", requestID,
            "method", r.Method,
            "path", r.URL.Path,
            "remote_addr", r.RemoteAddr,
            "user_agent", r.UserAgent(),
        )

        ctx := obs.ContextWithLogger(r.Context(), logger)
        r = r.WithContext(ctx)

        start := time.Now()
        ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
        next.ServeHTTP(ww, r)

        logger.Info("request completed",
            "status", ww.Status(),
            "bytes", ww.BytesWritten(),
            "duration_ms", time.Since(start).Milliseconds(),
        )
    })
}
```

**Acceptance Criteria:**
- ✅ All logs use slog (no log.Printf)
- ✅ JSON format in production (LOG_FORMAT=json)
- ✅ Request ID included in all request-scoped logs
- ✅ User ID included when authenticated
- ✅ Configurable log level (LOG_LEVEL env)

---

#### Task 3.2: Expanded Prometheus Metrics (2 hours)
**Assignee:** Infra Solutions Engineer

**Files to Modify:**
- `/home/user/athena/internal/metrics/metrics.go` (expand metrics)

**New Metrics to Add:**
```go
package metrics

import (
    "sync/atomic"
    "time"
)

var (
    // HTTP metrics
    httpRequestsTotal     int64
    httpRequestsFailed    int64
    httpRequestDurationMs int64

    // Database metrics
    dbConnectionsOpen     int64
    dbConnectionsIdle     int64
    dbQueriesTotal        int64
    dbQueryDurationMs     int64
    dbTransactionsTotal   int64

    // IPFS metrics
    ipfsPinningTotal      int64
    ipfsPinningFailed     int64
    ipfsPinDurationMs     int64
    ipfsGatewayRequests   int64

    // Queue metrics
    queueDepth            int64
    queueProcessed        int64
    queueFailed           int64

    // IOTA metrics (new)
    iotaWalletsCreated    int64
    iotaPaymentsConfirmed int64
    iotaPaymentsPending   int64

    // Virus scanner metrics
    virusScanTotal        int64
    virusScanInfected     int64
    virusScanClean        int64
)

// HTTP metrics
func IncHTTPRequests()           { atomic.AddInt64(&httpRequestsTotal, 1) }
func IncHTTPRequestsFailed()     { atomic.AddInt64(&httpRequestsFailed, 1) }
func RecordHTTPDuration(ms int64) { atomic.AddInt64(&httpRequestDurationMs, ms) }

// Database metrics
func SetDBConnectionsOpen(n int) { atomic.StoreInt64(&dbConnectionsOpen, int64(n)) }
func SetDBConnectionsIdle(n int) { atomic.StoreInt64(&dbConnectionsIdle, int64(n)) }
func IncDBQueries()              { atomic.AddInt64(&dbQueriesTotal, 1) }
func RecordDBQueryDuration(ms int64) { atomic.AddInt64(&dbQueryDurationMs, ms) }

// IPFS metrics
func IncIPFSPinning()            { atomic.AddInt64(&ipfsPinningTotal, 1) }
func IncIPFSPinningFailed()      { atomic.AddInt64(&ipfsPinningFailed, 1) }
func RecordIPFSPinDuration(ms int64) { atomic.AddInt64(&ipfsPinDurationMs, ms) }

// IOTA metrics
func IncIOTAWalletsCreated()     { atomic.AddInt64(&iotaWalletsCreated, 1) }
func IncIOTAPaymentsConfirmed()  { atomic.AddInt64(&iotaPaymentsConfirmed, 1) }
func SetIOTAPaymentsPending(n int) { atomic.StoreInt64(&iotaPaymentsPending, int64(n)) }

// Virus scanner metrics
func IncVirusScan()              { atomic.AddInt64(&virusScanTotal, 1) }
func IncVirusScanInfected()      { atomic.AddInt64(&virusScanInfected, 1) }
func IncVirusScanClean()         { atomic.AddInt64(&virusScanClean, 1) }

// Update Handler() to export all metrics
func Handler(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "text/plain; version=0.0.4")

    // HTTP metrics
    fmt.Fprintf(w, "# TYPE athena_http_requests_total counter\n")
    fmt.Fprintf(w, "athena_http_requests_total %d\n", atomic.LoadInt64(&httpRequestsTotal))

    // ... (export all metrics)
}
```

**Acceptance Criteria:**
- ✅ All critical paths instrumented (HTTP, DB, IPFS, IOTA)
- ✅ Metrics follow Prometheus naming conventions
- ✅ Histograms for durations (future: use prometheus/client_golang)
- ✅ Dashboard-ready (Grafana template provided)

---

#### Task 3.3: OpenTelemetry Distributed Tracing (2-3 hours)
**Assignee:** Infra Solutions Engineer

**Files to Create:**
- `/home/user/athena/internal/obs/tracing.go` - Tracer initialization
- `/home/user/athena/internal/middleware/tracing.go` - HTTP trace middleware

**Dependencies:**
```bash
go get go.opentelemetry.io/otel
go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp
go get go.opentelemetry.io/otel/sdk/trace
go get go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp
```

**Implementation:** `/home/user/athena/internal/obs/tracing.go`
```go
package obs

import (
    "context"
    "fmt"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
    "go.opentelemetry.io/otel/sdk/resource"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

var tracer = otel.Tracer("athena")

// InitTracing initializes OpenTelemetry tracing
func InitTracing(serviceName, otlpEndpoint string) (func(context.Context) error, error) {
    exporter, err := otlptracehttp.New(context.Background(),
        otlptracehttp.WithEndpoint(otlpEndpoint),
        otlptracehttp.WithInsecure(), // Use TLS in production
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create trace exporter: %w", err)
    }

    res, err := resource.New(context.Background(),
        resource.WithAttributes(
            semconv.ServiceNameKey.String(serviceName),
        ),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create resource: %w", err)
    }

    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithResource(res),
    )
    otel.SetTracerProvider(tp)

    return tp.Shutdown, nil
}

// Tracer returns the global tracer
func Tracer() trace.Tracer {
    return tracer
}
```

**Middleware:** `/home/user/athena/internal/middleware/tracing.go`
```go
package middleware

import (
    "net/http"

    "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func Tracing(next http.Handler) http.Handler {
    return otelhttp.NewHandler(next, "http.server",
        otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
            return r.Method + " " + r.URL.Path
        }),
    )
}
```

**Usage in Services:**
```go
// Example: Trace video processing
ctx, span := obs.Tracer().Start(ctx, "encode_video")
defer span.End()

span.SetAttributes(
    attribute.String("video_id", videoID),
    attribute.String("codec", "h264"),
)

if err := encodeVideo(ctx, videoID); err != nil {
    span.RecordError(err)
    return err
}
```

**Acceptance Criteria:**
- ✅ Traces exported to OTLP endpoint (Jaeger, Tempo)
- ✅ End-to-end trace for video upload → processing → federation
- ✅ Trace context propagated across service boundaries
- ✅ Errors recorded in spans
- ✅ Configurable via OTEL_EXPORTER_OTLP_ENDPOINT env

---

### Epic 3 Summary

**Total Effort:** 6-8 hours
**Files Created/Modified:** 8 files
**Observability Coverage:**
- ✅ Structured logging with slog (JSON format)
- ✅ Request IDs in all logs
- ✅ 30+ Prometheus metrics
- ✅ Distributed tracing with OpenTelemetry
- ✅ Production-ready monitoring

---

## Epic 4: Go-Atlas Configuration

**Priority:** MEDIUM
**Effort:** 2-3 hours
**Owner:** Infra Solutions Engineer

### Objective

Replace shell script-based migrations with Go-Atlas for professional schema management, migration linting, and CI/CD integration.

### Tasks

#### Task 4.1: Atlas Configuration (1 hour)
**Assignee:** Infra Solutions Engineer

**Files to Create:**
- `/home/user/athena/atlas.hcl` - Atlas configuration
- `/home/user/athena/schema.hcl` - Declarative schema (optional, keep SQL migrations)

**Implementation:** `/home/user/athena/atlas.hcl`
```hcl
env "local" {
  src = "file://migrations"
  url = "postgres://athena_user:password@localhost:5432/athena?sslmode=disable"

  dev = "docker://postgres/15/dev"

  migration {
    dir = "file://migrations"
  }

  lint {
    destructive {
      error = true
    }
    data_depend {
      error = true
    }
  }
}

env "dev" {
  src = "file://migrations"
  url = getenv("DATABASE_URL")

  dev = "docker://postgres/15/dev"

  migration {
    dir = "file://migrations"
  }
}

env "production" {
  src = "file://migrations"
  url = getenv("DATABASE_URL")

  dev = "postgres://athena_shadow:password@localhost:5433/athena_shadow?sslmode=disable"

  migration {
    dir = "file://migrations"
    revisions_schema = "atlas_migrations"
  }

  lint {
    destructive {
      error = true
    }
    data_depend {
      error = true
    }
    latest = 1 # Require migration order
  }
}
```

**Acceptance Criteria:**
- ✅ Atlas config supports local, dev, production envs
- ✅ Shadow DB for production migration testing
- ✅ Lint checks prevent destructive changes
- ✅ Migration directory: `migrations/`

---

#### Task 4.2: CI/CD Integration (1 hour)
**Assignee:** Infra Solutions Engineer

**Files to Create:**
- `.github/workflows/atlas-lint.yml`

**GitHub Actions Workflow:**
```yaml
name: Atlas Migration Lint

on:
  pull_request:
    paths:
      - 'migrations/**'

jobs:
  lint:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:15
        env:
          POSTGRES_USER: athena_user
          POSTGRES_PASSWORD: password
          POSTGRES_DB: athena
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5

    steps:
      - uses: actions/checkout@v3

      - name: Install Atlas
        run: |
          curl -sSf https://atlasgo.sh | sh

      - name: Lint Migrations
        run: |
          atlas migrate lint \
            --dir "file://migrations" \
            --dev-url "postgres://athena_user:password@localhost:5432/athena?sslmode=disable" \
            --latest 1

      - name: Validate Migration Plan
        run: |
          atlas migrate apply \
            --dir "file://migrations" \
            --url "postgres://athena_user:password@localhost:5432/athena?sslmode=disable" \
            --dry-run
```

**Acceptance Criteria:**
- ✅ PR checks lint migrations before merge
- ✅ Destructive changes blocked
- ✅ Migration plan validated
- ✅ Fast feedback (< 2 minutes)

---

#### Task 4.3: Documentation (30 min)
**Assignee:** Infra Solutions Engineer

**Files to Create:**
- `/home/user/athena/docs/migrations.md`

**Content:**
```markdown
# Database Migrations with Atlas

## Local Development

### Create a new migration
atlas migrate diff add_feature_x \
  --dir "file://migrations" \
  --to "file://schema.hcl" \
  --dev-url "docker://postgres/15/dev"

### Apply migrations
atlas migrate apply \
  --dir "file://migrations" \
  --url "postgres://user:pass@localhost:5432/athena?sslmode=disable"

### Lint migrations
atlas migrate lint \
  --dir "file://migrations" \
  --dev-url "docker://postgres/15/dev"

## Production

### Generate migration plan
atlas migrate apply \
  --dir "file://migrations" \
  --url "$DATABASE_URL" \
  --dry-run

### Apply with approval
atlas migrate apply \
  --dir "file://migrations" \
  --url "$DATABASE_URL" \
  --tx-mode file
```

**Acceptance Criteria:**
- ✅ Clear instructions for developers
- ✅ Examples for common tasks
- ✅ Production workflow documented

---

### Epic 4 Summary

**Total Effort:** 2-3 hours
**Files Created:** 4 files
**Benefits:**
- ✅ Professional migration management
- ✅ Automated lint checks in CI
- ✅ Shadow DB testing for production
- ✅ Prevention of destructive changes

---

## Risk Assessment

### Blockers

1. **IOTA Node Access** (MEDIUM)
   - **Risk:** No IOTA node endpoint for development/testing
   - **Mitigation:** Use IOTA testnet (free), mock client for unit tests
   - **Fallback:** Stub implementation with feature flag (ENABLE_IOTA=false)

2. **OpenTelemetry Backend** (LOW)
   - **Risk:** No OTLP endpoint for trace collection
   - **Mitigation:** Optional feature (OTEL_EXPORTER_OTLP_ENDPOINT), use Jaeger all-in-one for dev
   - **Fallback:** Tracing disabled if no endpoint configured

3. **Test Flakiness** (LOW)
   - **Risk:** External dependencies (IOTA, ActivityPub federation) may cause flaky tests
   - **Mitigation:** Mock all external HTTP calls, use test doubles for IOTA client
   - **Acceptance:** Integration tests allowed to be skipped if external services unavailable

### Dependencies Between Tasks

```mermaid
graph TD
    A[IOTA Schema] --> B[IOTA Client]
    B --> C[IOTA Service]
    C --> D[Payment Worker]
    D --> E[HTTP API]
    E --> F[Integration Tests]

    G[VideoObject Builder] --> H[Create Activity]
    H --> I[Comment Federation]
    I --> J[Federation Tests]

    K[Structured Logging] --> L[Metrics Expansion]
    L --> M[Tracing]

    N[Atlas Config] --> O[CI Integration]
    O --> P[Documentation]
```

**Critical Path:** IOTA Schema → Client → Service (8 hours)
**Parallel Tracks:**
- ActivityPub (6 hours)
- Observability (6 hours)
- Atlas (2 hours)

**Resource Allocation:**
- Decentralized Systems Security Expert: IOTA (full sprint)
- Federation Protocol Auditor: ActivityPub (full sprint)
- Infra Solutions Engineer: Observability + Atlas (full sprint)
- API Edge Tester: IOTA API + tests (3 hours)
- Golang Test Guardian: All tests (full sprint)
- Go Backend Reviewer: Reviews (ongoing)
- Decentralized Video PM: Coordination (ongoing)

---

## Quality Gates

### Sprint 2 Completion Criteria

**Must Have (Blocking):**
- ✅ IOTA wallet creation working
- ✅ IOTA payment detection working
- ✅ Videos federate to ActivityPub on upload
- ✅ Structured logging in all modules
- ✅ Prometheus metrics expanded
- ✅ Go-Atlas configured
- ✅ All tests passing in CI
- ✅ Test coverage: 80%+

**Should Have (Non-Blocking):**
- ✅ IOTA payment worker deployed
- ✅ Comment federation working
- ✅ OpenTelemetry tracing configured
- ✅ Atlas CI checks enabled

**Nice to Have (Sprint 3):**
- ⏭️ IOTA withdrawal functionality
- ⏭️ Video Update activities (federation)
- ⏭️ Video Delete activities (federation)
- ⏭️ Grafana dashboard templates

### Performance Benchmarks

| Operation | Target Latency (p50) | Target Latency (p99) |
|-----------|----------------------|----------------------|
| IOTA wallet creation | < 100ms | < 500ms |
| Payment intent creation | < 50ms | < 200ms |
| VideoObject build | < 10ms | < 50ms |
| Create activity delivery | < 200ms | < 1s |
| Structured log write | < 1ms | < 5ms |

### Test Coverage Targets

| Module | Target Coverage |
|--------|-----------------|
| IOTA Payments | 80%+ |
| ActivityPub Video | 85%+ |
| Observability | 70%+ |
| Atlas Config | 100% (config files) |

---

## Success Metrics

### Sprint 2 KPIs

**Feature Completeness:**
- ✅ IOTA payments: 100% (wallet, payment, detection)
- ✅ ActivityPub video federation: 100% (Create activity)
- ✅ Observability: 90% (logging, metrics, tracing)
- ✅ Atlas: 100% (config, CI, docs)

**Test Coverage:**
- Target: 80% overall
- IOTA: 80%+
- ActivityPub: 85%+
- Observability: 70%+

**Build Health:**
- ✅ All builds passing
- ✅ Zero critical lint errors
- ✅ Migration lint passing

**Integration:**
- ✅ Videos appear on Mastodon/PeerTube
- ✅ IOTA payments confirmed on testnet
- ✅ Traces visible in Jaeger

---

## Sprint 2 Execution Timeline

**Day 1 (8 hours):**
- IOTA schema + domain models (1h)
- IOTA client + wallet (4h)
- VideoObject builder (2h)
- Atlas configuration (1h)

**Day 2 (8 hours):**
- IOTA service (3h)
- Create activity delivery (2h)
- Structured logging (2h)
- Atlas CI integration (1h)

**Day 3 (8 hours):**
- IOTA payment worker (2h)
- IOTA HTTP API (2h)
- Comment federation (2h)
- Metrics expansion (2h)

**Day 4 (6 hours):**
- OpenTelemetry tracing (3h)
- Integration tests (2h)
- Documentation (1h)

**Total:** 30 hours (7 agents, ~4 days with reviews)

---

## Post-Sprint Actions

### Sprint 2 Review

1. **Demo:** IOTA payment flow end-to-end
2. **Demo:** Video federated to Mastodon
3. **Demo:** Trace video upload → processing → federation in Jaeger
4. **Metrics Review:** Prometheus dashboard

### Sprint 3 Planning (Preview)

**Potential Sprint 3 Objectives:**
1. **IPFS Pinning Strategy** (4-6 hours) - Scoring algorithm, auto-unpin
2. **Storage Tier Management** (4-6 hours) - Promotion/demotion logic
3. **IOTA Withdrawals** (3-4 hours) - Creator payouts
4. **Video Update/Delete Federation** (3-4 hours) - Complete ActivityPub CRUD
5. **Grafana Dashboards** (2-3 hours) - Pre-built monitoring templates

**Estimated Duration:** 16-23 hours (2-3 days)

---

## Sign-Off

**Sprint 2 Status:** ✅ **APPROVED FOR EXECUTION**

**Ready to Begin:** Yes
**Blocked:** No
**Resources Allocated:** 7 agents

**Signed:**
Decentralized Video PM
November 16, 2025

**Next Review:** Sprint 2 completion (estimated November 20, 2025)
