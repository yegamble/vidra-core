//go:build integration

// Integration tests: prove the E2EE schema invariants (migration 0058) and the
// one-time-key claim atomicity against a REAL PostgreSQL (migrations applied).
// Run via `make test-integration` (backend-integration CI):
//
//	docker compose --profile core up -d postgres redis migrate
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration -race ./internal/e2ee/...
package e2ee_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/e2ee"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func connect(t *testing.T) (*store.Store, *sqlcgen.Queries) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	return st, st.Queries()
}

func seedUser(ctx context.Context, t *testing.T, q *sqlcgen.Queries) uuid.UUID {
	t.Helper()
	suf := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username: "e2ee_" + suf, Email: "e2ee_" + suf + "@example.test", PasswordHash: "x", Role: "user",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u.ID
}

// TestE2EESchemaCiphertextOnly is the §3 storage-invariant guard at the schema
// level: the E2EE tables carry ONLY the documented columns — no plaintext
// column (body/content/text/plaintext) exists anywhere, and the conversations
// table gained the immutable encrypted flag.
func TestE2EESchemaCiphertextOnly(t *testing.T) {
	st, _ := connect(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	want := map[string][]string{
		"e2ee_devices":       {"created_at", "device_name", "id", "identity_key", "last_seen_at", "signing_key", "user_id"},
		"e2ee_one_time_keys": {"claimed_at", "created_at", "device_id", "id", "key", "key_id"},
		"e2ee_messages":      {"ciphertext", "conversation_id", "created_at", "expires_at", "id", "message_type", "recipient_device_id", "sender_device_id"},
	}
	for table, expect := range want {
		rows, err := st.Pool.Query(ctx,
			`SELECT column_name FROM information_schema.columns WHERE table_name = $1 ORDER BY column_name`, table)
		if err != nil {
			t.Fatalf("columns of %s: %v", table, err)
		}
		var got []string
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				t.Fatalf("scan: %v", err)
			}
			got = append(got, c)
		}
		rows.Close()
		if strings.Join(got, ",") != strings.Join(expect, ",") {
			t.Errorf("%s columns = %v, want exactly %v (no plaintext column may exist)", table, got, expect)
		}
		for _, c := range got {
			for _, banned := range []string{"body", "plaintext", "content"} {
				if c == banned {
					t.Errorf("%s carries banned plaintext-ish column %q", table, c)
				}
			}
		}
	}

	var encryptedCol bool
	if err := st.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_name = 'conversations' AND column_name = 'encrypted')`).Scan(&encryptedCol); err != nil {
		t.Fatalf("conversations.encrypted probe: %v", err)
	}
	if !encryptedCol {
		t.Error("conversations.encrypted column missing")
	}
}

// TestE2EEClaimAtomicityUnderConcurrentClaims proves the single-use guarantee
// with REAL concurrency: N goroutines race ClaimE2EEOneTimeKey against one
// device holding K < N keys — exactly K claims succeed and no key is ever
// handed out twice (FOR UPDATE SKIP LOCKED).
func TestE2EEClaimAtomicityUnderConcurrentClaims(t *testing.T) {
	_, q := connect(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	userID := seedUser(ctx, t, q)
	dev, err := q.CreateE2EEDevice(ctx, sqlcgen.CreateE2EEDeviceParams{
		UserID: userID, DeviceName: "racer", IdentityKey: "idk", SigningKey: "sgk",
	})
	if err != nil {
		t.Fatalf("CreateE2EEDevice: %v", err)
	}
	const (
		keys     = 20
		claimers = 35
	)
	for i := 0; i < keys; i++ {
		if err := q.InsertE2EEOneTimeKey(ctx, sqlcgen.InsertE2EEOneTimeKeyParams{
			DeviceID: dev.ID, KeyID: fmt.Sprintf("k-%02d", i), Key: fmt.Sprintf("v-%02d", i),
		}); err != nil {
			t.Fatalf("InsertE2EEOneTimeKey: %v", err)
		}
	}

	var (
		mu      sync.Mutex
		claimed []string
		misses  int
	)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < claimers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			row, err := q.ClaimE2EEOneTimeKey(ctx, dev.ID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				misses++
				return
			}
			claimed = append(claimed, row.KeyID)
		}()
	}
	close(start)
	wg.Wait()

	if len(claimed) != keys || misses != claimers-keys {
		t.Fatalf("claims = %d, misses = %d; want exactly %d claims and %d misses",
			len(claimed), misses, keys, claimers-keys)
	}
	seen := map[string]bool{}
	for _, k := range claimed {
		if seen[k] {
			t.Fatalf("key %q was claimed by more than one concurrent claimer", k)
		}
		seen[k] = true
	}
	// The pool is fully drained: nothing left to claim.
	if n, err := q.CountUnclaimedE2EEOneTimeKeys(ctx, dev.ID); err != nil || n != 0 {
		t.Fatalf("unclaimed after race = (%d, %v), want (0, nil)", n, err)
	}
}

// TestE2EERoundTripAndExpiryOnRealPG drives the service against real PG: an
// encrypted conversation, a byte-identical ciphertext round trip, the read
// filter for an already-expired envelope, and the sweeper's hard delete.
func TestE2EERoundTripAndExpiryOnRealPG(t *testing.T) {
	_, q := connect(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	svc := e2ee.NewService(q)
	ada := seedUser(ctx, t, q)
	bob := seedUser(ctx, t, q)
	adaDev, err := svc.RegisterDevice(ctx, ada, "ada-laptop", "idk-a", "sgk-a")
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	bobDev, err := svc.RegisterDevice(ctx, bob, "bob-phone", "idk-b", "sgk-b")
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	conv, err := svc.StartConversation(ctx, ada, bob)
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	if again, err := svc.StartConversation(ctx, bob, ada); err != nil || again.ID != conv.ID {
		t.Fatalf("start-or-get = (%v, %v), want %v", again.ID, err, conv.ID)
	}

	// Byte-identical ciphertext round trip (§3), including exotic characters.
	cipher := "AwogK2V4b3RpYyI6ICLwn5mIIiwgIn4hQCMkJV4mKigpXysiff8="
	if _, err := svc.Send(ctx, ada, conv.ID, adaDev.ID,
		[]e2ee.Envelope{{RecipientDeviceID: bobDev.ID, MessageType: 0, Ciphertext: cipher}}, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, err := svc.ListMessages(ctx, bob, conv.ID, 10, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("ListMessages = (%d, %v), want 1 envelope", len(got), err)
	}
	if got[0].Ciphertext != cipher {
		t.Fatalf("ciphertext round trip mismatch: got %q want %q", got[0].Ciphertext, cipher)
	}
	if got[0].SenderUserID != ada || got[0].RecipientDeviceID != bobDev.ID {
		t.Fatalf("envelope attribution = %+v", got[0])
	}
	// The sender's read is empty: no envelope is addressed to ada's device.
	if mine, err := svc.ListMessages(ctx, ada, conv.ID, 10, 0); err != nil || len(mine) != 0 {
		t.Fatalf("sender read = (%d, %v), want 0", len(mine), err)
	}

	// Insert an ALREADY-EXPIRED envelope directly (a service send can only
	// stamp >= 30s ahead): the read filters it immediately, the sweeper
	// hard-deletes it, and the live envelope survives both.
	if _, err := q.CreateE2EEMessage(ctx, sqlcgen.CreateE2EEMessageParams{
		ConversationID: conv.ID, SenderDeviceID: adaDev.ID, RecipientDeviceID: bobDev.ID,
		MessageType: 1, Ciphertext: "expired-envelope",
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	}); err != nil {
		t.Fatalf("CreateE2EEMessage(expired): %v", err)
	}
	got, err = svc.ListMessages(ctx, bob, conv.ID, 10, 0)
	if err != nil || len(got) != 1 || got[0].Ciphertext != cipher {
		t.Fatalf("read with expired row = (%+v, %v), want only the live envelope", got, err)
	}
	if n, err := svc.SweepExpired(ctx, 100); err != nil || n < 1 {
		t.Fatalf("SweepExpired = (%d, %v), want >= 1", n, err)
	}
	rows, err := svc.ListMessages(ctx, bob, conv.ID, 10, 0)
	if err != nil || len(rows) != 1 || rows[0].Ciphertext != cipher {
		t.Fatalf("post-sweep read = (%+v, %v), want only the live envelope", rows, err)
	}
}
