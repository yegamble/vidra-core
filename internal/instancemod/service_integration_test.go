//go:build integration

// Integration test: proves the muted_instances and blocked_instances tables
// (migration 0051) persist against a REAL PostgreSQL. Run via
// `make test-integration`.
package instancemod_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/instancemod"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestInstanceModerationPersists(t *testing.T) {
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
	svc := instancemod.NewService(q)

	suf := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username:     "imod_" + suf,
		Email:        "imod_" + suf + "@example.test",
		PasswordHash: "x",
		Role:         "user",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Per-user instance mute round trip (normalized, idempotent).
	domain := "muted-" + suf + ".example"
	if err := svc.MuteInstance(ctx, u.ID, strings.ToUpper(domain)); err != nil {
		t.Fatalf("MuteInstance: %v", err)
	}
	if err := svc.MuteInstance(ctx, u.ID, domain); err != nil {
		t.Fatalf("re-MuteInstance: %v", err)
	}
	mutes, err := svc.ListMutedInstances(ctx, u.ID, 20, 0)
	if err != nil {
		t.Fatalf("ListMutedInstances: %v", err)
	}
	if len(mutes) != 1 || mutes[0].Domain != domain {
		t.Fatalf("mutes = %+v, want [%s]", mutes, domain)
	}
	if err := svc.UnmuteInstance(ctx, u.ID, domain); err != nil {
		t.Fatalf("UnmuteInstance: %v", err)
	}
	if mutes, _ := svc.ListMutedInstances(ctx, u.ID, 20, 0); len(mutes) != 0 {
		t.Errorf("after unmute mutes = %+v, want none", mutes)
	}

	// Admin blocklist round trip (re-block refreshes the reason).
	blocked := "blocked-" + suf + ".example"
	t.Cleanup(func() { _ = svc.UnblockInstance(context.Background(), blocked) })
	if err := svc.BlockInstance(ctx, u.ID, blocked, "first"); err != nil {
		t.Fatalf("BlockInstance: %v", err)
	}
	if err := svc.BlockInstance(ctx, u.ID, blocked, "refreshed"); err != nil {
		t.Fatalf("re-BlockInstance: %v", err)
	}
	if isBlocked, err := q.IsInstanceBlocked(ctx, blocked); err != nil || !isBlocked {
		t.Fatalf("IsInstanceBlocked = %v, %v; want true", isBlocked, err)
	}
	list, err := svc.ListBlockedInstances(ctx, 100, 0)
	if err != nil {
		t.Fatalf("ListBlockedInstances: %v", err)
	}
	found := false
	for _, b := range list {
		if b.Domain == blocked {
			found = true
			if b.Reason != "refreshed" {
				t.Errorf("reason = %q, want refreshed", b.Reason)
			}
			if b.BlockedBy == nil || *b.BlockedBy != u.ID {
				t.Errorf("blocked_by = %v, want %s", b.BlockedBy, u.ID)
			}
		}
	}
	if !found {
		t.Fatalf("blocked domain %s not in list (%d entries)", blocked, len(list))
	}
	if err := svc.UnblockInstance(ctx, blocked); err != nil {
		t.Fatalf("UnblockInstance: %v", err)
	}
	if isBlocked, _ := q.IsInstanceBlocked(ctx, blocked); isBlocked {
		t.Error("still blocked after unblock")
	}
}
