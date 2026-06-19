package video

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory video.Repository. Each video remembers its channel's
// owner so GetVideoByID can return the joined owner_id.
type fakeRepo struct {
	videos map[uuid.UUID]sqlcgen.GetVideoByIDRow
	owner  uuid.UUID
}

func newFakeRepo(owner uuid.UUID) *fakeRepo {
	return &fakeRepo{videos: map[uuid.UUID]sqlcgen.GetVideoByIDRow{}, owner: owner}
}

func (f *fakeRepo) CreateVideo(_ context.Context, a sqlcgen.CreateVideoParams) (sqlcgen.Video, error) {
	v := sqlcgen.Video{
		ID: uuid.New(), ChannelID: a.ChannelID, Title: a.Title,
		Description: a.Description, Privacy: a.Privacy, State: "draft",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.videos[v.ID] = sqlcgen.GetVideoByIDRow{
		ID: v.ID, ChannelID: v.ChannelID, Title: v.Title, Description: v.Description,
		Privacy: v.Privacy, State: v.State, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
		OwnerID: f.owner,
	}
	return v, nil
}

func (f *fakeRepo) GetVideoByID(_ context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	v, ok := f.videos[id]
	if !ok {
		return sqlcgen.GetVideoByIDRow{}, errors.New("not found")
	}
	return v, nil
}

func TestCreateDraftDefaultsToDraftState(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner))
	ch := uuid.New()
	v, err := svc.CreateDraft(context.Background(), ch, CreateInput{Title: "Hello", Privacy: "private"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if v.State != "draft" {
		t.Errorf("state = %q, want draft", v.State)
	}
	if v.ChannelID != ch || v.Title != "Hello" || v.Privacy != "private" {
		t.Errorf("unexpected video: %+v", v)
	}
}

func TestGetByIDReturnsOwner(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner))
	created, _ := svc.CreateDraft(context.Background(), uuid.New(), CreateInput{Title: "T", Privacy: "public"})

	got, err := svc.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.OwnerID != owner {
		t.Errorf("owner = %v, want %v", got.OwnerID, owner)
	}
}

func TestGetByIDNotFound(t *testing.T) {
	svc := NewService(newFakeRepo(uuid.New()))
	if _, err := svc.GetByID(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
