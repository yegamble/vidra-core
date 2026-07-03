package live

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory live.Repository.
type fakeRepo struct {
	rows   map[uuid.UUID]sqlcgen.GetLiveStreamByIDRow
	hashes map[uuid.UUID]string
	owner  uuid.UUID
}

func newFakeRepo(owner uuid.UUID) *fakeRepo {
	return &fakeRepo{rows: map[uuid.UUID]sqlcgen.GetLiveStreamByIDRow{}, hashes: map[uuid.UUID]string{}, owner: owner}
}

func (f *fakeRepo) CreateLiveStream(_ context.Context, a sqlcgen.CreateLiveStreamParams) (sqlcgen.CreateLiveStreamRow, error) {
	id := uuid.New()
	now := time.Now()
	f.rows[id] = sqlcgen.GetLiveStreamByIDRow{
		ID: id, ChannelID: a.ChannelID, Title: a.Title, Description: a.Description,
		Privacy: a.Privacy, State: "offline", Permanent: a.Permanent, ReplayEnabled: a.ReplayEnabled,
		CreatedAt: now, UpdatedAt: now,
		OwnerID: f.owner, ChannelHandle: "ch", ChannelDisplayName: "Ch",
	}
	f.hashes[id] = a.StreamKeyHash
	return sqlcgen.CreateLiveStreamRow{
		ID: id, ChannelID: a.ChannelID, Title: a.Title, Description: a.Description,
		Privacy: a.Privacy, State: "offline", Permanent: a.Permanent, ReplayEnabled: a.ReplayEnabled,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (f *fakeRepo) UpdateLiveStream(_ context.Context, a sqlcgen.UpdateLiveStreamParams) (sqlcgen.UpdateLiveStreamRow, error) {
	r, ok := f.rows[a.ID]
	if !ok {
		return sqlcgen.UpdateLiveStreamRow{}, errors.New("not found")
	}
	r.Title, r.Description, r.Privacy, r.Permanent, r.ReplayEnabled = a.Title, a.Description, a.Privacy, a.Permanent, a.ReplayEnabled
	r.UpdatedAt = time.Now()
	f.rows[a.ID] = r
	return sqlcgen.UpdateLiveStreamRow{
		ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
		Privacy: r.Privacy, State: r.State, Permanent: r.Permanent, ReplayEnabled: r.ReplayEnabled,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, nil
}

func (f *fakeRepo) GetLiveStreamByID(_ context.Context, id uuid.UUID) (sqlcgen.GetLiveStreamByIDRow, error) {
	r, ok := f.rows[id]
	if !ok {
		return sqlcgen.GetLiveStreamByIDRow{}, errors.New("not found")
	}
	return r, nil
}

func (f *fakeRepo) ListLiveStreamsByChannel(_ context.Context, channelID uuid.UUID) ([]sqlcgen.ListLiveStreamsByChannelRow, error) {
	var out []sqlcgen.ListLiveStreamsByChannelRow
	for _, r := range f.rows {
		if r.ChannelID == channelID {
			out = append(out, sqlcgen.ListLiveStreamsByChannelRow{
				ID: r.ID, ChannelID: r.ChannelID, Title: r.Title, Description: r.Description,
				Privacy: r.Privacy, State: r.State, Permanent: r.Permanent, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			})
		}
	}
	return out, nil
}

func (f *fakeRepo) UpdateLiveStreamKey(_ context.Context, a sqlcgen.UpdateLiveStreamKeyParams) error {
	f.hashes[a.ID] = a.StreamKeyHash
	return nil
}

func (f *fakeRepo) GetLiveStreamByKeyHash(_ context.Context, h string) (sqlcgen.GetLiveStreamByKeyHashRow, error) {
	for id, hash := range f.hashes {
		if hash == h {
			r := f.rows[id]
			return sqlcgen.GetLiveStreamByKeyHashRow{ID: id, ChannelID: r.ChannelID, Permanent: r.Permanent, State: r.State}, nil
		}
	}
	return sqlcgen.GetLiveStreamByKeyHashRow{}, errors.New("not found")
}

func (f *fakeRepo) SetLiveStreamState(_ context.Context, a sqlcgen.SetLiveStreamStateParams) error {
	if r, ok := f.rows[a.ID]; ok {
		r.State = a.State
		f.rows[a.ID] = r
	}
	return nil
}

func (f *fakeRepo) DeleteLiveStream(_ context.Context, id uuid.UUID) (int64, error) {
	if _, ok := f.rows[id]; ok {
		delete(f.rows, id)
		delete(f.hashes, id)
		return 1, nil
	}
	return 0, nil
}

func hashOf(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func TestCreateReturnsKeyOnceAndStoresHash(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo(uuid.New())
	svc := NewService(repo)
	ch := uuid.New()

	stream, key, err := svc.Create(ctx, ch, CreateInput{Title: "My Live", Permanent: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if key == "" {
		t.Fatal("Create returned an empty stream key")
	}
	if stream.Title != "My Live" || !stream.Permanent || stream.State != "offline" || stream.Privacy != "public" {
		t.Fatalf("unexpected stream: %+v", stream)
	}
	// Only the hash of the key is stored (never the raw key).
	if got := repo.hashes[stream.ID]; got != hashOf(key) {
		t.Errorf("stored hash = %q, want sha256(key) %q", got, hashOf(key))
	}

	// A second create yields a different key + hash.
	_, key2, _ := svc.Create(ctx, ch, CreateInput{Title: "Another"})
	if key2 == key {
		t.Error("two creates produced the same stream key")
	}
}

func TestGetAndListAndDelete(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo)
	ch := uuid.New()

	s1, _, _ := svc.Create(ctx, ch, CreateInput{Title: "One", Privacy: "unlisted"})
	svc.Create(ctx, ch, CreateInput{Title: "Two"}) //nolint

	got, err := svc.Get(ctx, s1.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.OwnerID != owner || got.Title != "One" || got.Privacy != "unlisted" {
		t.Fatalf("Get returned %+v", got)
	}
	if _, err := svc.Get(ctx, uuid.New()); err != ErrNotFound {
		t.Errorf("Get(unknown) = %v, want ErrNotFound", err)
	}

	list, _ := svc.ListByChannel(ctx, ch)
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}

	if err := svc.Delete(ctx, s1.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, s1.ID); err != ErrNotFound {
		t.Errorf("after delete Get = %v, want ErrNotFound", err)
	}
}

func TestIngestStartStop(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo(uuid.New())
	svc := NewService(repo)

	// A one-shot stream: start → live, stop → ended.
	s, key, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "One-shot"})
	if _, err := svc.StartIngest(ctx, key); err != nil {
		t.Fatalf("StartIngest: %v", err)
	}
	if got := repo.rows[s.ID].State; got != StateLive {
		t.Errorf("state after start = %q, want live", got)
	}
	if _, err := svc.StopIngest(ctx, key); err != nil {
		t.Fatalf("StopIngest: %v", err)
	}
	if got := repo.rows[s.ID].State; got != StateEnded {
		t.Errorf("state after stop (one-shot) = %q, want ended", got)
	}

	// A permanent stream returns to offline on stop (reusable).
	p, pkey, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "Permanent", Permanent: true})
	svc.StartIngest(ctx, pkey) //nolint
	if _, err := svc.StopIngest(ctx, pkey); err != nil {
		t.Fatalf("StopIngest (permanent): %v", err)
	}
	if got := repo.rows[p.ID].State; got != StateOffline {
		t.Errorf("state after stop (permanent) = %q, want offline", got)
	}

	// An unknown key is denied.
	if _, err := svc.StartIngest(ctx, "not-a-real-key"); err != ErrNotFound {
		t.Errorf("StartIngest(unknown) = %v, want ErrNotFound", err)
	}
}

func TestStartStopByIdentity(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo(uuid.New())
	svc := NewService(repo)

	s, key, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "Show"})

	// First publish presents the raw key → needs a rename to the id.
	id, needsRename, err := svc.StartByIdentity(ctx, key)
	if err != nil || id != s.ID || !needsRename {
		t.Fatalf("StartByIdentity(key) = (%v,%v,%v), want (%v,true,nil)", id, needsRename, err, s.ID)
	}
	// Re-invocation with the renamed id must be idempotent and NOT ask to rename
	// again (avoids the nginx-rtmp redirect loop).
	id2, needsRename2, err := svc.StartByIdentity(ctx, s.ID.String())
	if err != nil || id2 != s.ID || needsRename2 {
		t.Fatalf("StartByIdentity(id) = (%v,%v,%v), want (%v,false,nil)", id2, needsRename2, err, s.ID)
	}
	if got := repo.rows[s.ID].State; got != StateLive {
		t.Errorf("state = %q, want live", got)
	}

	// Stop resolves by the stream id (post-rename media-server path).
	st, err := svc.StopByIdentity(ctx, s.ID.String())
	if err != nil || st.ID != s.ID {
		t.Fatalf("StopByIdentity(id) = (%+v,%v), want the stream", st, err)
	}
	if got := repo.rows[s.ID].State; got != StateEnded {
		t.Errorf("state after stop = %q, want ended", got)
	}
	// An unknown identity (neither a live id nor a known key) is denied.
	if _, err := svc.StopByIdentity(ctx, uuid.New().String()); err != ErrNotFound {
		t.Errorf("StopByIdentity(unknown id) = %v, want ErrNotFound", err)
	}
	if _, _, err := svc.StartByIdentity(ctx, "not-a-key"); err != ErrNotFound {
		t.Errorf("StartByIdentity(garbage) = %v, want ErrNotFound", err)
	}
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo(uuid.New())
	svc := NewService(repo)

	s, _, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "Before", Privacy: "public"})
	updated, err := svc.Update(ctx, s.ID, UpdateInput{
		Title: "After", Description: "d", Privacy: "unlisted", Permanent: true, ReplayEnabled: true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Title != "After" || updated.Privacy != "unlisted" || !updated.Permanent || !updated.ReplayEnabled {
		t.Fatalf("updated = %+v, want the edited fields", updated)
	}
	// Persisted (a subsequent Get reflects it).
	got, _ := svc.Get(ctx, s.ID)
	if got.Title != "After" || !got.ReplayEnabled {
		t.Errorf("Get after update = %+v, want persisted edits", got)
	}
	// Empty privacy defaults to public.
	back, _ := svc.Update(ctx, s.ID, UpdateInput{Title: "x"})
	if back.Privacy != "public" {
		t.Errorf("default privacy = %q, want public", back.Privacy)
	}
	// Unknown id → ErrNotFound.
	if _, err := svc.Update(ctx, uuid.New(), UpdateInput{Title: "x"}); err != ErrNotFound {
		t.Errorf("Update(unknown) = %v, want ErrNotFound", err)
	}
}

func TestRegenerateKey(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo(uuid.New())
	svc := NewService(repo)

	s, key1, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "Live"})
	key2, err := svc.RegenerateKey(ctx, s.ID)
	if err != nil {
		t.Fatalf("RegenerateKey: %v", err)
	}
	if key2 == "" || key2 == key1 {
		t.Errorf("regenerated key = %q (old %q), want a new non-empty key", key2, key1)
	}
	if got := repo.hashes[s.ID]; got != hashOf(key2) {
		t.Errorf("stored hash = %q, want sha256(new key) %q", got, hashOf(key2))
	}
}
