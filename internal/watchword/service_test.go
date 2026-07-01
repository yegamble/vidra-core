package watchword

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory watchword.Repository enforcing the case-insensitive
// uniqueness of the real unique index.
type fakeRepo struct {
	words   map[uuid.UUID]sqlcgen.WatchedWord
	order   []uuid.UUID
	present map[string]bool // lower(word) -> exists
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{words: map[uuid.UUID]sqlcgen.WatchedWord{}, present: map[string]bool{}}
}

func (f *fakeRepo) CreateWatchedWord(_ context.Context, a sqlcgen.CreateWatchedWordParams) (sqlcgen.WatchedWord, error) {
	if f.present[strings.ToLower(a.Word)] {
		return sqlcgen.WatchedWord{}, &pgconn.PgError{Code: "23505"} // unique violation
	}
	w := sqlcgen.WatchedWord{ID: uuid.New(), Word: a.Word, CreatedBy: a.CreatedBy, CreatedAt: time.Now()}
	f.words[w.ID] = w
	f.order = append(f.order, w.ID)
	f.present[strings.ToLower(a.Word)] = true
	return w, nil
}

func (f *fakeRepo) ListWatchedWords(_ context.Context, _ sqlcgen.ListWatchedWordsParams) ([]sqlcgen.ListWatchedWordsRow, error) {
	var rows []sqlcgen.ListWatchedWordsRow
	for i := len(f.order) - 1; i >= 0; i-- { // newest first
		w, ok := f.words[f.order[i]]
		if !ok {
			continue
		}
		rows = append(rows, sqlcgen.ListWatchedWordsRow{ID: w.ID, Word: w.Word, CreatedAt: w.CreatedAt})
	}
	return rows, nil
}

func (f *fakeRepo) DeleteWatchedWord(_ context.Context, id uuid.UUID) (int64, error) {
	w, ok := f.words[id]
	if !ok {
		return 0, nil
	}
	delete(f.words, id)
	delete(f.present, strings.ToLower(w.Word))
	return 1, nil
}

func TestAddListDelete(t *testing.T) {
	svc := NewService(newFakeRepo())
	ctx := context.Background()
	mod := uuid.New()

	w1, err := svc.Add(ctx, "spam", mod)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := svc.Add(ctx, "abuse", mod); err != nil {
		t.Fatalf("Add abuse: %v", err)
	}

	items, err := svc.List(ctx, 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 || items[0].Word != "abuse" || items[1].Word != "spam" {
		t.Fatalf("list = %+v, want [abuse, spam] newest-first", items)
	}

	if err := svc.Delete(ctx, w1.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Idempotent delete of an absent id.
	if err := svc.Delete(ctx, uuid.New()); err != nil {
		t.Errorf("idempotent Delete: %v", err)
	}
	if items, _ := svc.List(ctx, 20, 0); len(items) != 1 || items[0].Word != "abuse" {
		t.Errorf("list after delete = %+v, want [abuse]", items)
	}
}

func TestAddDuplicate(t *testing.T) {
	svc := NewService(newFakeRepo())
	ctx := context.Background()
	if _, err := svc.Add(ctx, "Spam", uuid.New()); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// A case-insensitive duplicate → ErrAlreadyExists.
	if _, err := svc.Add(ctx, "spam", uuid.New()); err != ErrAlreadyExists {
		t.Errorf("duplicate Add = %v, want ErrAlreadyExists", err)
	}
}
