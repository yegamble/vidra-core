package block

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory block.Repository.
type fakeRepo struct {
	blocks   map[string]bool // "blocker|blocked"
	blockErr error           // returned by BlockUser when set (e.g. an FK violation)
}

func key(blocker, blocked uuid.UUID) string { return blocker.String() + "|" + blocked.String() }

func (f *fakeRepo) BlockUser(_ context.Context, a sqlcgen.BlockUserParams) (int64, error) {
	if f.blockErr != nil {
		return 0, f.blockErr
	}
	if f.blocks == nil {
		f.blocks = map[string]bool{}
	}
	k := key(a.BlockerID, a.BlockedID)
	if f.blocks[k] {
		return 0, nil
	}
	f.blocks[k] = true
	return 1, nil
}

func (f *fakeRepo) UnblockUser(_ context.Context, a sqlcgen.UnblockUserParams) (int64, error) {
	k := key(a.BlockerID, a.BlockedID)
	if f.blocks[k] {
		delete(f.blocks, k)
		return 1, nil
	}
	return 0, nil
}

func (f *fakeRepo) ListBlockedUsers(_ context.Context, a sqlcgen.ListBlockedUsersParams) ([]sqlcgen.ListBlockedUsersRow, error) {
	var rows []sqlcgen.ListBlockedUsersRow
	for k := range f.blocks {
		if k[:len(a.BlockerID.String())] == a.BlockerID.String() {
			blocked := uuid.MustParse(k[len(a.BlockerID.String())+1:])
			rows = append(rows, sqlcgen.ListBlockedUsersRow{BlockedID: blocked, Username: "u", DisplayName: "U"})
		}
	}
	return rows, nil
}

func (f *fakeRepo) IsBlockedBetween(_ context.Context, a sqlcgen.IsBlockedBetweenParams) (bool, error) {
	return f.blocks[key(a.BlockerID, a.BlockedID)] || f.blocks[key(a.BlockedID, a.BlockerID)], nil
}

func TestBlockUnblockList(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()
	blocker, target := uuid.New(), uuid.New()

	if items, _, _ := svc.List(ctx, blocker, 20, 0); len(items) != 0 {
		t.Fatalf("blocked before = %d, want 0", len(items))
	}
	if err := svc.Block(ctx, blocker, target); err != nil {
		t.Fatalf("Block: %v", err)
	}
	// Idempotent re-block.
	if err := svc.Block(ctx, blocker, target); err != nil {
		t.Fatalf("re-Block: %v", err)
	}
	items, _, err := svc.List(ctx, blocker, 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].UserID != target {
		t.Fatalf("blocked = %+v, want [target]", items)
	}
	if err := svc.Unblock(ctx, blocker, target); err != nil {
		t.Fatalf("Unblock: %v", err)
	}
	// Idempotent unblock.
	if err := svc.Unblock(ctx, blocker, target); err != nil {
		t.Errorf("idempotent Unblock: %v", err)
	}
	if items, _, _ := svc.List(ctx, blocker, 20, 0); len(items) != 0 {
		t.Errorf("blocked after unblock = %d, want 0", len(items))
	}
}

func TestBlockSelf(t *testing.T) {
	svc := NewService(&fakeRepo{})
	id := uuid.New()
	if err := svc.Block(context.Background(), id, id); err != ErrCannotBlockSelf {
		t.Errorf("self-block err = %v, want ErrCannotBlockSelf", err)
	}
}

func TestBlockUnknownTarget(t *testing.T) {
	svc := NewService(&fakeRepo{blockErr: &pgconn.PgError{Code: "23503"}})
	if err := svc.Block(context.Background(), uuid.New(), uuid.New()); err != ErrUserNotFound {
		t.Errorf("unknown-target err = %v, want ErrUserNotFound", err)
	}
}

func TestIsBlockedBetweenIsSymmetric(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()
	ada, bob := uuid.New(), uuid.New()

	if blocked, _ := svc.IsBlockedBetween(ctx, ada, bob); blocked {
		t.Fatal("unexpected block before any block")
	}
	// Ada blocks Bob → blocked in BOTH directions.
	if err := svc.Block(ctx, ada, bob); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if blocked, _ := svc.IsBlockedBetween(ctx, ada, bob); !blocked {
		t.Error("ada→bob should be blocked")
	}
	if blocked, _ := svc.IsBlockedBetween(ctx, bob, ada); !blocked {
		t.Error("bob→ada should also be blocked (symmetric)")
	}
}

func (f *fakeRepo) CountBlockedUsers(ctx context.Context, blockerID uuid.UUID) (int64, error) {
	rows, err := f.ListBlockedUsers(ctx, sqlcgen.ListBlockedUsersParams{BlockerID: blockerID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}
