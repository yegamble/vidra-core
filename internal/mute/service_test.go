package mute

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory mute.Repository.
type fakeRepo struct {
	mutes   map[string]bool // "muter|muted"
	muteErr error           // returned by MuteAccount when set (e.g. an FK violation)
}

func key(muter, muted uuid.UUID) string { return muter.String() + "|" + muted.String() }

func (f *fakeRepo) MuteAccount(_ context.Context, a sqlcgen.MuteAccountParams) (int64, error) {
	if f.muteErr != nil {
		return 0, f.muteErr
	}
	if f.mutes == nil {
		f.mutes = map[string]bool{}
	}
	k := key(a.MuterID, a.MutedID)
	if f.mutes[k] {
		return 0, nil
	}
	f.mutes[k] = true
	return 1, nil
}

func (f *fakeRepo) UnmuteAccount(_ context.Context, a sqlcgen.UnmuteAccountParams) (int64, error) {
	k := key(a.MuterID, a.MutedID)
	if f.mutes[k] {
		delete(f.mutes, k)
		return 1, nil
	}
	return 0, nil
}

func (f *fakeRepo) ListMutedAccounts(_ context.Context, a sqlcgen.ListMutedAccountsParams) ([]sqlcgen.ListMutedAccountsRow, error) {
	var rows []sqlcgen.ListMutedAccountsRow
	for k := range f.mutes {
		if k[:len(a.MuterID.String())] == a.MuterID.String() {
			muted := uuid.MustParse(k[len(a.MuterID.String())+1:])
			rows = append(rows, sqlcgen.ListMutedAccountsRow{MutedID: muted, Username: "u", DisplayName: "U"})
		}
	}
	return rows, nil
}

func TestMuteUnmuteList(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	ctx := context.Background()
	muter, target := uuid.New(), uuid.New()

	if items, _ := svc.List(ctx, muter, 20, 0); len(items) != 0 {
		t.Fatalf("muted before = %d, want 0", len(items))
	}
	if err := svc.Mute(ctx, muter, target); err != nil {
		t.Fatalf("Mute: %v", err)
	}
	// Idempotent re-mute.
	if err := svc.Mute(ctx, muter, target); err != nil {
		t.Fatalf("re-Mute: %v", err)
	}
	items, err := svc.List(ctx, muter, 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].UserID != target {
		t.Fatalf("muted = %+v, want [target]", items)
	}
	if err := svc.Unmute(ctx, muter, target); err != nil {
		t.Fatalf("Unmute: %v", err)
	}
	// Idempotent unmute.
	if err := svc.Unmute(ctx, muter, target); err != nil {
		t.Errorf("idempotent Unmute: %v", err)
	}
	if items, _ := svc.List(ctx, muter, 20, 0); len(items) != 0 {
		t.Errorf("muted after unmute = %d, want 0", len(items))
	}
}

func TestMuteSelf(t *testing.T) {
	svc := NewService(&fakeRepo{})
	id := uuid.New()
	if err := svc.Mute(context.Background(), id, id); err != ErrCannotMuteSelf {
		t.Errorf("self-mute err = %v, want ErrCannotMuteSelf", err)
	}
}

func TestMuteUnknownTarget(t *testing.T) {
	svc := NewService(&fakeRepo{muteErr: &pgconn.PgError{Code: "23503"}})
	if err := svc.Mute(context.Background(), uuid.New(), uuid.New()); err != ErrUserNotFound {
		t.Errorf("unknown-target err = %v, want ErrUserNotFound", err)
	}
}
