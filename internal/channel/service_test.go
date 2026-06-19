package channel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory channel.Repository keyed by lowercased handle.
type fakeRepo struct {
	byHandle map[string]sqlcgen.Channel
}

func newFakeRepo() *fakeRepo { return &fakeRepo{byHandle: map[string]sqlcgen.Channel{}} }

func (f *fakeRepo) CreateChannel(_ context.Context, a sqlcgen.CreateChannelParams) (sqlcgen.Channel, error) {
	key := strings.ToLower(a.Handle)
	if _, ok := f.byHandle[key]; ok {
		return sqlcgen.Channel{}, &pgconn.PgError{Code: "23505"}
	}
	ch := sqlcgen.Channel{
		ID: uuid.New(), OwnerID: a.OwnerID, Handle: a.Handle,
		DisplayName: a.DisplayName, Description: a.Description,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.byHandle[key] = ch
	return ch, nil
}

func (f *fakeRepo) GetChannelByHandle(_ context.Context, lowerHandle string) (sqlcgen.Channel, error) {
	ch, ok := f.byHandle[strings.ToLower(lowerHandle)]
	if !ok {
		return sqlcgen.Channel{}, errors.New("not found")
	}
	return ch, nil
}

func (f *fakeRepo) ListChannelsByOwner(_ context.Context, ownerID uuid.UUID) ([]sqlcgen.Channel, error) {
	var out []sqlcgen.Channel
	for _, ch := range f.byHandle {
		if ch.OwnerID == ownerID {
			out = append(out, ch)
		}
	}
	return out, nil
}

func TestCreateChannel(t *testing.T) {
	svc := NewService(newFakeRepo())
	owner := uuid.New()
	ch, err := svc.Create(context.Background(), owner, CreateInput{Handle: "ada_makes", DisplayName: "Ada Makes"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ch.OwnerID != owner || ch.Handle != "ada_makes" {
		t.Errorf("unexpected channel: %+v", ch)
	}
}

func TestCreateChannelDuplicateIsConflict(t *testing.T) {
	svc := NewService(newFakeRepo())
	owner := uuid.New()
	if _, err := svc.Create(context.Background(), owner, CreateInput{Handle: "ada", DisplayName: "Ada"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	// Different owner, same handle (case-insensitive) → conflict.
	_, err := svc.Create(context.Background(), uuid.New(), CreateInput{Handle: "ADA", DisplayName: "Ada2"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestGetByHandle(t *testing.T) {
	svc := NewService(newFakeRepo())
	owner := uuid.New()
	_, _ = svc.Create(context.Background(), owner, CreateInput{Handle: "ada", DisplayName: "Ada"})

	got, err := svc.GetByHandle(context.Background(), "ADA")
	if err != nil {
		t.Fatalf("GetByHandle: %v", err)
	}
	if got.Handle != "ada" {
		t.Errorf("handle = %q, want ada", got.Handle)
	}

	if _, err := svc.GetByHandle(context.Background(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListOwn(t *testing.T) {
	svc := NewService(newFakeRepo())
	owner := uuid.New()
	_, _ = svc.Create(context.Background(), owner, CreateInput{Handle: "one", DisplayName: "One"})
	_, _ = svc.Create(context.Background(), owner, CreateInput{Handle: "two", DisplayName: "Two"})
	_, _ = svc.Create(context.Background(), uuid.New(), CreateInput{Handle: "other", DisplayName: "Other"})

	chans, err := svc.ListOwn(context.Background(), owner)
	if err != nil {
		t.Fatalf("ListOwn: %v", err)
	}
	if len(chans) != 2 {
		t.Errorf("got %d channels, want 2", len(chans))
	}
}
