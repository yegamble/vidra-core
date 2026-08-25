package live

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/observability"
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

func (f *fakeRepo) ListLiveStreamsByChannel(_ context.Context, a sqlcgen.ListLiveStreamsByChannelParams) ([]sqlcgen.ListLiveStreamsByChannelRow, error) {
	channelID := a.ChannelID
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
			return sqlcgen.GetLiveStreamByKeyHashRow{ID: id, ChannelID: r.ChannelID, Permanent: r.Permanent, State: r.State, OwnerID: r.OwnerID}, nil
		}
	}
	return sqlcgen.GetLiveStreamByKeyHashRow{}, errors.New("not found")
}

func (f *fakeRepo) CountLiveStreamsLive(_ context.Context) (int64, error) {
	var n int64
	for _, r := range f.rows {
		if r.State == "live" {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) CountLiveStreamsLiveByOwner(_ context.Context, ownerID uuid.UUID) (int64, error) {
	var n int64
	for _, r := range f.rows {
		if r.State == "live" && r.OwnerID == ownerID {
			n++
		}
	}
	return n, nil
}

// ListOverdueLiveStreams mirrors the SQL: live sessions whose non-NULL
// started_at is strictly before the cutoff.
func (f *fakeRepo) ListOverdueLiveStreams(_ context.Context, cutoff pgtype.Timestamptz) ([]sqlcgen.ListOverdueLiveStreamsRow, error) {
	var out []sqlcgen.ListOverdueLiveStreamsRow
	for _, r := range f.rows {
		if r.State == "live" && r.StartedAt.Valid && r.StartedAt.Time.Before(cutoff.Time) {
			out = append(out, sqlcgen.ListOverdueLiveStreamsRow{ID: r.ID, Permanent: r.Permanent, StartedAt: r.StartedAt})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Time.Before(out[j].StartedAt.Time) })
	return out, nil
}

func (f *fakeRepo) SetLiveStreamState(_ context.Context, a sqlcgen.SetLiveStreamStateParams) error {
	if r, ok := f.rows[a.ID]; ok {
		switch {
		case a.State == "live" && r.State != "live":
			r.StartedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		case a.State != "live":
			r.StartedAt = pgtype.Timestamptz{}
		}
		r.State = a.State
		f.rows[a.ID] = r
	}
	return nil
}

func (f *fakeRepo) ListLivePublicStreams(_ context.Context, a sqlcgen.ListLivePublicStreamsParams) ([]sqlcgen.ListLivePublicStreamsRow, error) {
	var rows []sqlcgen.GetLiveStreamByIDRow
	for _, r := range f.rows {
		if r.State == "live" && r.Privacy == "public" {
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].StartedAt.Time.After(rows[j].StartedAt.Time) })
	lo := int(a.ResultOffset)
	if lo > len(rows) {
		lo = len(rows)
	}
	hi := lo + int(a.ResultLimit)
	if hi > len(rows) {
		hi = len(rows)
	}
	out := make([]sqlcgen.ListLivePublicStreamsRow, 0, hi-lo)
	for _, r := range rows[lo:hi] {
		out = append(out, sqlcgen.ListLivePublicStreamsRow{
			ID: r.ID, Title: r.Title, Description: r.Description, StartedAt: r.StartedAt,
			ChannelHandle: r.ChannelHandle, ChannelDisplayName: r.ChannelDisplayName,
		})
	}
	return out, nil
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

	list, _, _ := svc.ListByChannel(ctx, ch, 100, 0)
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

// setOwner rewrites a stream row's owner so per-user cap tests can model
// streams belonging to different users inside one fake repo (whose default
// stamps every row with the same owner).
func (f *fakeRepo) setOwner(id, owner uuid.UUID) {
	r := f.rows[id]
	r.OwnerID = owner
	f.rows[id] = r
}

// TestStartByIdentityInstanceLiveCap proves the live_max_instance_lives cap at
// the publish boundary (config-parity W11): at cap the publish is refused with
// ErrInstanceLiveLimit; 0 means unlimited; an already-live session's re-assert
// (the post-rename re-invocation) is never re-counted or rejected; and freeing
// a slot admits the next publish.
func TestStartByIdentityInstanceLiveCap(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo(uuid.New())
	limit := int64(0)
	svc := NewService(repo, WithMaxInstanceLivesFunc(func() int64 { return limit }))

	a, aKey, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "A"})
	_, bKey, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "B"})
	c, cKey, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "C", Permanent: true})

	// 0 = unlimited: both go live.
	if _, _, err := svc.StartByIdentity(ctx, aKey); err != nil {
		t.Fatalf("start A (unlimited): %v", err)
	}
	if _, _, err := svc.StartByIdentity(ctx, bKey); err != nil {
		t.Fatalf("start B (unlimited): %v", err)
	}

	// Cap of 2 with 2 already live: C is refused.
	limit = 2
	if _, _, err := svc.StartByIdentity(ctx, cKey); !errors.Is(err, ErrInstanceLiveLimit) {
		t.Fatalf("start C at cap = %v, want ErrInstanceLiveLimit", err)
	}
	// A re-assert of the already-live A (post-rename re-invocation) is allowed
	// and not double-counted.
	if _, needsRename, err := svc.StartByIdentity(ctx, a.ID.String()); err != nil || needsRename {
		t.Fatalf("re-assert live A = (rename=%v, %v), want (false, nil)", needsRename, err)
	}
	// Ending A frees a slot: C is admitted.
	if _, err := svc.StopByIdentity(ctx, a.ID.String()); err != nil {
		t.Fatalf("stop A: %v", err)
	}
	if _, _, err := svc.StartByIdentity(ctx, cKey); err != nil {
		t.Fatalf("start C after slot freed: %v", err)
	}
	if got := repo.rows[c.ID].State; got != StateLive {
		t.Errorf("C state = %q, want live", got)
	}
}

// TestStartByIdentityUserLiveCap proves the per-user live_max_user_lives cap:
// a user at their cap is refused (ErrUserLiveLimit) while another user still
// publishes freely.
func TestStartByIdentityUserLiveCap(t *testing.T) {
	ctx := context.Background()
	ada, bob := uuid.New(), uuid.New()
	repo := newFakeRepo(ada)
	svc := NewService(repo, WithMaxUserLivesFunc(func() int64 { return 1 }))

	_, ada1Key, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "Ada 1"})
	ada2, ada2Key, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "Ada 2"})
	bobSt, bobKey, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "Bob"})
	repo.setOwner(bobSt.ID, bob)

	if _, _, err := svc.StartByIdentity(ctx, ada1Key); err != nil {
		t.Fatalf("start Ada 1: %v", err)
	}
	// Ada's second simultaneous live is refused.
	if _, _, err := svc.StartByIdentity(ctx, ada2Key); !errors.Is(err, ErrUserLiveLimit) {
		t.Fatalf("start Ada 2 = %v, want ErrUserLiveLimit", err)
	}
	if got := repo.rows[ada2.ID].State; got != "offline" {
		t.Errorf("refused stream state = %q, want offline (never flipped)", got)
	}
	// Bob is under HIS cap: allowed.
	if _, _, err := svc.StartByIdentity(ctx, bobKey); err != nil {
		t.Fatalf("start Bob: %v", err)
	}
}

// TestSweepOverdueLive proves the live_max_duration_secs watchdog: over-limit
// sessions are force-closed (one-shot → ended, permanent → offline, exactly
// like an ingest-stop), under-limit sessions and pre-0076 sessions without a
// started_at are untouched, 0 disables the sweep, and each force-close is
// audited. The clock is injected so the test controls "now".
func TestSweepOverdueLive(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo(uuid.New())
	auditor := &fakeAuditor{}
	maxSecs := int64(0)
	now := time.Now()
	svc := NewService(repo,
		WithMaxDurationSecsFunc(func() int64 { return maxSecs }),
		WithNowFunc(func() time.Time { return now }),
		WithAuditor(auditor),
	)

	oneShot, k1, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "One-shot"})
	perm, k2, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "Permanent", Permanent: true})
	fresh, k3, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "Fresh"})
	legacy, k4, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "Legacy"})
	for _, k := range []string{k1, k2, k3, k4} {
		if _, err := svc.StartIngest(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	// Backdate one-shot + permanent past the limit; leave fresh recent; strip
	// legacy's started_at (a session live since before migration 0076).
	backdate := func(id uuid.UUID, ago time.Duration) {
		r := repo.rows[id]
		r.StartedAt = pgtype.Timestamptz{Time: now.Add(-ago), Valid: true}
		repo.rows[id] = r
	}
	backdate(oneShot.ID, 2*time.Hour)
	backdate(perm.ID, 3*time.Hour)
	backdate(fresh.ID, time.Minute)
	{
		r := repo.rows[legacy.ID]
		r.StartedAt = pgtype.Timestamptz{}
		repo.rows[legacy.ID] = r
	}

	// Limit 0: the sweep is a no-op.
	if n, err := svc.SweepOverdueLive(ctx); err != nil || n != 0 {
		t.Fatalf("sweep with no limit = (%d, %v), want (0, nil)", n, err)
	}

	// One hour limit: exactly the two backdated sessions close.
	maxSecs = 3600
	n, err := svc.SweepOverdueLive(ctx)
	if err != nil {
		t.Fatalf("SweepOverdueLive: %v", err)
	}
	if n != 2 {
		t.Fatalf("closed %d sessions, want 2", n)
	}
	if got := repo.rows[oneShot.ID].State; got != StateEnded {
		t.Errorf("one-shot state = %q, want ended", got)
	}
	if got := repo.rows[perm.ID].State; got != StateOffline {
		t.Errorf("permanent state = %q, want offline (reusable)", got)
	}
	if got := repo.rows[fresh.ID].State; got != StateLive {
		t.Errorf("fresh state = %q, want still live", got)
	}
	if got := repo.rows[legacy.ID].State; got != StateLive {
		t.Errorf("legacy (no started_at) state = %q, want still live (nothing truthful to measure)", got)
	}
	if len(auditor.events) != 2 {
		t.Fatalf("audit events = %d, want 2 force-closes", len(auditor.events))
	}
	for _, ev := range auditor.events {
		if ev.Action != observability.ActionLiveForceClose || ev.Result != observability.ResultSuccess || ev.Actor.Kind != "system" {
			t.Errorf("audit event = %+v, want a system content.live.force_close success", ev)
		}
	}

	// A second sweep finds nothing (closed sessions cleared started_at/state).
	if n, err := svc.SweepOverdueLive(ctx); err != nil || n != 0 {
		t.Errorf("second sweep = (%d, %v), want (0, nil)", n, err)
	}
}

// TestListLivePublicExcludesNonPublicAndOffline proves the public "Live now"
// listing surfaces ONLY currently-live PUBLIC streams (never unlisted/private,
// never offline/ended), stamps started_at on go-live, and honours limit/offset.
func TestListLivePublicExcludesNonPublicAndOffline(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo(uuid.New())
	svc := NewService(repo)
	chID := uuid.New()

	mk := func(title, privacy string, goLive bool) uuid.UUID {
		st, key, err := svc.Create(ctx, chID, CreateInput{Title: title, Privacy: privacy})
		if err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
		if goLive {
			if _, err := svc.StartIngest(ctx, key); err != nil {
				t.Fatalf("start %q: %v", title, err)
			}
		}
		return st.ID
	}

	pub1 := mk("Public A", "public", true)
	mk("Public offline", "public", false)
	mk("Unlisted live", "unlisted", true)
	mk("Private live", "private", true)
	pub2 := mk("Public B", "public", true)

	cards, _, err := svc.ListLivePublic(ctx, 30, 0)
	if err != nil {
		t.Fatalf("ListLivePublic: %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2 (only public+live); %+v", len(cards), cards)
	}
	got := map[uuid.UUID]bool{}
	for _, c := range cards {
		got[c.ID] = true
		if c.StartedAt == nil {
			t.Errorf("card %q missing started_at", c.Title)
		}
	}
	if !got[pub1] || !got[pub2] {
		t.Errorf("expected both public live streams %s,%s; got %+v", pub1, pub2, got)
	}

	// Pagination: limit clamps the page; offset skips.
	page1, _, _ := svc.ListLivePublic(ctx, 1, 0)
	if len(page1) != 1 {
		t.Fatalf("limit=1 returned %d, want 1", len(page1))
	}
	page2, _, _ := svc.ListLivePublic(ctx, 1, 1)
	if len(page2) != 1 {
		t.Fatalf("limit=1 offset=1 returned %d, want 1", len(page2))
	}
	if page1[0].ID == page2[0].ID {
		t.Errorf("offset did not advance: both pages returned %s", page1[0].ID)
	}

	// Ending a live stream drops it from the listing and clears started_at.
	st, err := svc.Get(ctx, pub1)
	if err != nil {
		t.Fatal(err)
	}
	// Flip pub1 offline via a permanent-style stop path: set it ended directly.
	if err := repo.SetLiveStreamState(ctx, sqlcgen.SetLiveStreamStateParams{ID: st.ID, State: "ended"}); err != nil {
		t.Fatal(err)
	}
	after, _, _ := svc.ListLivePublic(ctx, 30, 0)
	if len(after) != 1 {
		t.Fatalf("after ending pub1, got %d cards, want 1", len(after))
	}
	if after[0].ID != pub2 {
		t.Errorf("remaining card = %s, want pub2 %s", after[0].ID, pub2)
	}
	if ended, _ := svc.Get(ctx, pub1); ended.StartedAt != nil {
		t.Errorf("ended stream still has started_at = %v, want nil", ended.StartedAt)
	}
}

func (f *fakeRepo) CountLiveStreamsByChannel(ctx context.Context, channelID uuid.UUID) (int64, error) {
	rows, err := f.ListLiveStreamsByChannel(ctx, sqlcgen.ListLiveStreamsByChannelParams{ChannelID: channelID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *fakeRepo) CountLivePublicStreams(ctx context.Context) (int64, error) {
	rows, err := f.ListLivePublicStreams(ctx, sqlcgen.ListLivePublicStreamsParams{ResultLimit: 1 << 30})
	return int64(len(rows)), err
}
