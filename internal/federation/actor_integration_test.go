//go:build integration

// Integration test: proves actor-key minting persists against a REAL PostgreSQL
// (migrations applied). Run via `make test-integration` (backend-integration CI).
package federation_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestActorKeyMintingPersists(t *testing.T) {
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
	defer st.Close()
	q := st.Queries()

	suf := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username:     "fed_" + suf,
		Email:        "fed_" + suf + "@example.test",
		PasswordHash: "x",
		Role:         "user",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	svc := federation.NewService(q, federation.WithBaseURL("https://videos.example"))

	// Mint on first fetch; the key is served and persisted.
	a1, err := svc.AccountActor(ctx, u.Username)
	if err != nil {
		t.Fatalf("AccountActor (mint): %v", err)
	}
	if !strings.Contains(a1.PublicKey.PublicKeyPem, "BEGIN PUBLIC KEY") {
		t.Fatalf("no public key minted")
	}
	row, err := q.GetAccountActorKey(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetAccountActorKey after mint: %v", err)
	}
	if row.PublicKeyPem != a1.PublicKey.PublicKeyPem {
		t.Errorf("stored public key differs from served")
	}
	if !strings.Contains(row.PrivateKeyPem, "BEGIN PRIVATE KEY") {
		t.Errorf("private key not persisted (dev raw expected): %q", row.PrivateKeyPem)
	}

	// Idempotent: a second fetch reuses the stored key.
	a2, err := svc.AccountActor(ctx, u.Username)
	if err != nil {
		t.Fatalf("AccountActor (reuse): %v", err)
	}
	if a1.PublicKey.PublicKeyPem != a2.PublicKey.PublicKeyPem {
		t.Error("key was re-minted on the second fetch")
	}

	// Channel actors mint too.
	ch, err := q.CreateChannel(ctx, sqlcgen.CreateChannelParams{
		OwnerID: u.ID, Handle: "fedch_" + suf, DisplayName: "Fed Ch",
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	ca, err := svc.ChannelActor(ctx, ch.Handle)
	if err != nil {
		t.Fatalf("ChannelActor (mint): %v", err)
	}
	if ca.Type != "Group" {
		t.Errorf("channel actor type = %q, want Group", ca.Type)
	}
	if _, err := q.GetChannelActorKey(ctx, ch.ID); err != nil {
		t.Errorf("GetChannelActorKey after mint: %v", err)
	}
}
