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

func rowToVideo(r sqlcgen.GetVideoByIDRow) sqlcgen.Video {
	return sqlcgen.Video{
		ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
		Privacy: r.Privacy, State: r.State, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func (f *fakeRepo) ListVideosByChannel(_ context.Context, channelID uuid.UUID) ([]sqlcgen.Video, error) {
	var out []sqlcgen.Video
	for _, r := range f.videos {
		if r.ChannelID == channelID {
			out = append(out, rowToVideo(r))
		}
	}
	return out, nil
}

func (f *fakeRepo) ListPublicVideosByChannel(_ context.Context, channelID uuid.UUID) ([]sqlcgen.Video, error) {
	var out []sqlcgen.Video
	for _, r := range f.videos {
		if r.ChannelID == channelID && r.Privacy == "public" {
			out = append(out, rowToVideo(r))
		}
	}
	return out, nil
}

func (f *fakeRepo) UpdateVideo(_ context.Context, a sqlcgen.UpdateVideoParams) (sqlcgen.Video, error) {
	r, ok := f.videos[a.ID]
	if !ok {
		return sqlcgen.Video{}, errors.New("not found")
	}
	if a.Title != nil {
		r.Title = *a.Title
	}
	if a.Description != nil {
		r.Description = *a.Description
	}
	if a.Privacy != nil {
		r.Privacy = *a.Privacy
	}
	r.UpdatedAt = time.Now()
	f.videos[a.ID] = r
	return rowToVideo(r), nil
}

func (f *fakeRepo) DeleteVideo(_ context.Context, id uuid.UUID) error {
	delete(f.videos, id)
	return nil
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

func strptr(s string) *string { return &s }

func TestUpdateVideoOwnerNonOwnerNotFound(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "old", Privacy: "private"})

	// Owner partial update.
	up, err := svc.Update(ctx, owner, v.ID, UpdateInput{Title: strptr("new")})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if up.Title != "new" || up.Privacy != "private" {
		t.Errorf("unexpected update: %+v", up)
	}
	// Non-owner forbidden.
	if _, err := svc.Update(ctx, uuid.New(), v.ID, UpdateInput{Title: strptr("hax")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner err = %v, want ErrForbidden", err)
	}
	// Unknown id not found.
	if _, err := svc.Update(ctx, owner, uuid.New(), UpdateInput{Title: strptr("x")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown err = %v, want ErrNotFound", err)
	}
}

func TestDeleteVideoOwnerAndNonOwner(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})

	if err := svc.Delete(ctx, uuid.New(), v.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner delete err = %v, want ErrForbidden", err)
	}
	if err := svc.Delete(ctx, owner, v.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if _, err := svc.GetByID(ctx, v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v, want ErrNotFound", err)
	}
}

func TestListByChannelVsPublic(t *testing.T) {
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner))
	ctx := context.Background()
	ch := uuid.New()
	_, _ = svc.CreateDraft(ctx, ch, CreateInput{Title: "pub", Privacy: "public"})
	_, _ = svc.CreateDraft(ctx, ch, CreateInput{Title: "priv", Privacy: "private"})

	all, _ := svc.ListByChannel(ctx, ch)
	if len(all) != 2 {
		t.Errorf("ListByChannel = %d, want 2", len(all))
	}
	pub, _ := svc.ListPublicByChannel(ctx, ch)
	if len(pub) != 1 || pub[0].Privacy != "public" {
		t.Errorf("ListPublicByChannel = %+v, want 1 public", pub)
	}
}
