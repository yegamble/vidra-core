package playlist

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory playlist.Repository.
type fakeRepo struct {
	playlists map[uuid.UUID]sqlcgen.Playlist
	items     map[uuid.UUID][]uuid.UUID // playlistID -> ordered videoIDs
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{playlists: map[uuid.UUID]sqlcgen.Playlist{}, items: map[uuid.UUID][]uuid.UUID{}}
}

func (f *fakeRepo) CreatePlaylist(_ context.Context, a sqlcgen.CreatePlaylistParams) (sqlcgen.Playlist, error) {
	p := sqlcgen.Playlist{
		ID: uuid.New(), OwnerID: a.OwnerID, Title: a.Title, Description: a.Description,
		Visibility: a.Visibility, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.playlists[p.ID] = p
	return p, nil
}

func (f *fakeRepo) GetPlaylistByID(_ context.Context, id uuid.UUID) (sqlcgen.GetPlaylistByIDRow, error) {
	p, ok := f.playlists[id]
	if !ok {
		return sqlcgen.GetPlaylistByIDRow{}, errors.New("not found")
	}
	return sqlcgen.GetPlaylistByIDRow{
		ID: p.ID, OwnerID: p.OwnerID, Title: p.Title, Description: p.Description,
		Visibility: p.Visibility, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		VideoCount: int64(len(f.items[id])),
	}, nil
}

func (f *fakeRepo) ListPlaylistsByOwner(_ context.Context, ownerID uuid.UUID) ([]sqlcgen.ListPlaylistsByOwnerRow, error) {
	var rows []sqlcgen.ListPlaylistsByOwnerRow
	for _, p := range f.playlists {
		if p.OwnerID == ownerID {
			rows = append(rows, sqlcgen.ListPlaylistsByOwnerRow{
				ID: p.ID, OwnerID: p.OwnerID, Title: p.Title, Description: p.Description,
				Visibility: p.Visibility, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
				VideoCount: int64(len(f.items[p.ID])),
			})
		}
	}
	return rows, nil
}

func (f *fakeRepo) UpdatePlaylist(_ context.Context, a sqlcgen.UpdatePlaylistParams) (sqlcgen.Playlist, error) {
	p := f.playlists[a.ID]
	if a.Title != nil {
		p.Title = *a.Title
	}
	if a.Description != nil {
		p.Description = *a.Description
	}
	if a.Visibility != nil {
		p.Visibility = *a.Visibility
	}
	p.UpdatedAt = time.Now()
	f.playlists[a.ID] = p
	return p, nil
}

func (f *fakeRepo) DeletePlaylist(_ context.Context, id uuid.UUID) error {
	delete(f.playlists, id)
	delete(f.items, id)
	return nil
}

func (f *fakeRepo) AddPlaylistItem(_ context.Context, a sqlcgen.AddPlaylistItemParams) error {
	for _, v := range f.items[a.PlaylistID] {
		if v == a.VideoID {
			return nil // idempotent
		}
	}
	f.items[a.PlaylistID] = append(f.items[a.PlaylistID], a.VideoID)
	return nil
}

func (f *fakeRepo) RemovePlaylistItem(_ context.Context, a sqlcgen.RemovePlaylistItemParams) error {
	cur := f.items[a.PlaylistID]
	out := cur[:0:0]
	for _, v := range cur {
		if v != a.VideoID {
			out = append(out, v)
		}
	}
	f.items[a.PlaylistID] = out
	return nil
}

func (f *fakeRepo) ListPlaylistItems(_ context.Context, playlistID uuid.UUID) ([]sqlcgen.ListPlaylistItemsRow, error) {
	var rows []sqlcgen.ListPlaylistItemsRow
	for _, v := range f.items[playlistID] {
		rows = append(rows, sqlcgen.ListPlaylistItemsRow{ID: v, Title: "v-" + v.String()[:8]})
	}
	return rows, nil
}

func TestCreateGetAndItems(t *testing.T) {
	svc := NewService(newFakeRepo())
	ctx := context.Background()
	owner := uuid.New()

	p, err := svc.Create(ctx, owner, CreateInput{Title: "Faves", Visibility: "public"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := svc.GetByID(ctx, p.ID)
	if err != nil || got.VideoCount != 0 {
		t.Fatalf("GetByID = (%+v, %v), want count 0", got, err)
	}

	v1, v2 := uuid.New(), uuid.New()
	if err := svc.AddItem(ctx, owner, p.ID, v1); err != nil {
		t.Fatalf("AddItem v1: %v", err)
	}
	if err := svc.AddItem(ctx, owner, p.ID, v2); err != nil {
		t.Fatalf("AddItem v2: %v", err)
	}
	if err := svc.AddItem(ctx, owner, p.ID, v1); err != nil { // idempotent
		t.Fatalf("AddItem v1 again: %v", err)
	}
	items, err := svc.ListItems(ctx, p.ID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 2 || items[0].ID != v1 || items[1].ID != v2 {
		t.Fatalf("items = %+v, want [v1 v2] in order", items)
	}
	if got, _ := svc.GetByID(ctx, p.ID); got.VideoCount != 2 {
		t.Errorf("count after add = %d, want 2", got.VideoCount)
	}

	// Remove one.
	if err := svc.RemoveItem(ctx, owner, p.ID, v1); err != nil {
		t.Fatalf("RemoveItem: %v", err)
	}
	if items, _ := svc.ListItems(ctx, p.ID); len(items) != 1 || items[0].ID != v2 {
		t.Errorf("after remove = %+v, want [v2]", items)
	}
}

func TestOwnerOnlyMutations(t *testing.T) {
	svc := NewService(newFakeRepo())
	ctx := context.Background()
	owner, other := uuid.New(), uuid.New()
	p, _ := svc.Create(ctx, owner, CreateInput{Title: "Mine", Visibility: "private"})

	// Owner update works; non-owner is forbidden; unknown is not found.
	if _, err := svc.Update(ctx, owner, p.ID, UpdateInput{Title: strptr("Renamed")}); err != nil {
		t.Fatalf("owner update: %v", err)
	}
	if _, err := svc.Update(ctx, other, p.ID, UpdateInput{Title: strptr("Hax")}); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-owner update = %v, want ErrForbidden", err)
	}
	if _, err := svc.Update(ctx, owner, uuid.New(), UpdateInput{Title: strptr("x")}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown update = %v, want ErrNotFound", err)
	}

	// AddItem / RemoveItem / Delete enforce the same ownership.
	if err := svc.AddItem(ctx, other, p.ID, uuid.New()); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-owner add = %v, want ErrForbidden", err)
	}
	if err := svc.AddItem(ctx, owner, uuid.New(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("add to unknown = %v, want ErrNotFound", err)
	}
	if err := svc.Delete(ctx, other, p.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-owner delete = %v, want ErrForbidden", err)
	}
	if err := svc.Delete(ctx, owner, p.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if _, err := svc.GetByID(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after delete = %v, want ErrNotFound", err)
	}
}

func strptr(s string) *string { return &s }
