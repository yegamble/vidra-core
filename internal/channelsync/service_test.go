package channelsync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
	"github.com/vidra/vidra-core/internal/videoimport"
	"github.com/vidra/vidra-core/internal/ytdlp"
)

// ---- fakes ----------------------------------------------------------------

type fakeRepo struct {
	channels map[uuid.UUID]sqlcgen.Channel
	syncs    map[uuid.UUID]sqlcgen.ChannelSync
	seen     map[string]bool

	countErr  error
	createErr error

	claimed   []sqlcgen.ClaimDueChannelSyncsRow
	finished  []sqlcgen.FinishChannelSyncParams
	failed    []sqlcgen.FailChannelSyncParams
	triggered []uuid.UUID
	seenIns   []sqlcgen.InsertChannelSyncSeenParams
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		channels: map[uuid.UUID]sqlcgen.Channel{},
		syncs:    map[uuid.UUID]sqlcgen.ChannelSync{},
		seen:     map[string]bool{},
	}
}

func (f *fakeRepo) GetChannelByID(_ context.Context, id uuid.UUID) (sqlcgen.Channel, error) {
	ch, ok := f.channels[id]
	if !ok {
		return sqlcgen.Channel{}, errors.New("no rows")
	}
	return ch, nil
}

func (f *fakeRepo) CreateChannelSync(_ context.Context, arg sqlcgen.CreateChannelSyncParams) (sqlcgen.ChannelSync, error) {
	if f.createErr != nil {
		return sqlcgen.ChannelSync{}, f.createErr
	}
	for _, s := range f.syncs {
		if s.ChannelID == arg.ChannelID && s.ExternalChannelUrl == arg.ExternalChannelUrl {
			return sqlcgen.ChannelSync{}, &pgconn.PgError{Code: "23505"}
		}
	}
	s := sqlcgen.ChannelSync{
		ID:                 uuid.New(),
		ChannelID:          arg.ChannelID,
		UserID:             arg.UserID,
		ExternalChannelUrl: arg.ExternalChannelUrl,
		State:              "waiting_first_run",
		NextRunAt:          time.Now(),
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	f.syncs[s.ID] = s
	return s, nil
}

func (f *fakeRepo) ListChannelSyncsByUser(_ context.Context, userID uuid.UUID) ([]sqlcgen.ChannelSync, error) {
	var out []sqlcgen.ChannelSync
	for _, s := range f.syncs {
		if s.UserID == userID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeRepo) GetChannelSyncByID(_ context.Context, id uuid.UUID) (sqlcgen.ChannelSync, error) {
	s, ok := f.syncs[id]
	if !ok {
		return sqlcgen.ChannelSync{}, errors.New("no rows")
	}
	return s, nil
}

func (f *fakeRepo) CountChannelSyncsByUser(_ context.Context, userID uuid.UUID) (int64, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}
	var n int64
	for _, s := range f.syncs {
		if s.UserID == userID {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) DeleteChannelSync(_ context.Context, id uuid.UUID) error {
	delete(f.syncs, id)
	return nil
}

func (f *fakeRepo) TriggerChannelSyncNow(_ context.Context, id uuid.UUID) error {
	f.triggered = append(f.triggered, id)
	return nil
}

func (f *fakeRepo) ClaimDueChannelSyncs(_ context.Context, _ int32) ([]sqlcgen.ClaimDueChannelSyncsRow, error) {
	return f.claimed, nil
}

func (f *fakeRepo) FinishChannelSync(_ context.Context, arg sqlcgen.FinishChannelSyncParams) error {
	f.finished = append(f.finished, arg)
	return nil
}

func (f *fakeRepo) FailChannelSync(_ context.Context, arg sqlcgen.FailChannelSyncParams) error {
	f.failed = append(f.failed, arg)
	return nil
}

func (f *fakeRepo) InsertChannelSyncSeen(_ context.Context, arg sqlcgen.InsertChannelSyncSeenParams) (int64, error) {
	f.seenIns = append(f.seenIns, arg)
	key := arg.SyncID.String() + "|" + arg.ExternalID
	if f.seen[key] {
		return 0, nil
	}
	f.seen[key] = true
	return 1, nil
}

type draftCall struct {
	channelID uuid.UUID
	in        video.CreateInput
}

type fakeDrafter struct {
	calls []draftCall
	err   error
}

func (d *fakeDrafter) CreateDraft(_ context.Context, channelID uuid.UUID, in video.CreateInput) (sqlcgen.Video, error) {
	d.calls = append(d.calls, draftCall{channelID: channelID, in: in})
	if d.err != nil {
		return sqlcgen.Video{}, d.err
	}
	return sqlcgen.Video{ID: uuid.New(), ChannelID: channelID}, nil
}

type enqueueCall struct {
	videoID  uuid.UUID
	url      string
	resolver string
}

type fakeEnqueuer struct {
	calls []enqueueCall
	err   error
}

func (e *fakeEnqueuer) Enqueue(_ context.Context, videoID uuid.UUID, rawURL, resolver string) (sqlcgen.ImportJob, error) {
	e.calls = append(e.calls, enqueueCall{videoID: videoID, url: rawURL, resolver: resolver})
	if e.err != nil {
		return sqlcgen.ImportJob{}, e.err
	}
	return sqlcgen.ImportJob{ID: uuid.New(), VideoID: videoID}, nil
}

type fakeLister struct {
	entries []ytdlp.PlaylistEntry
	err     error
	gotURL  string
	gotN    int
}

func (l *fakeLister) Playlist(_ context.Context, channelURL string, limit int) ([]ytdlp.PlaylistEntry, error) {
	l.gotURL = channelURL
	l.gotN = limit
	if l.err != nil {
		return nil, l.err
	}
	return l.entries, nil
}

// enabledService wires a fully-enabled service over the given fakes.
func enabledService(repo Repository, d Drafter, e Enqueuer, l Lister, opts ...Option) *Service {
	base := []Option{WithEnabled(true), WithLister(l), WithInterval(time.Hour), WithBatch(15), WithMaxPerUser(5)}
	return NewService(repo, d, e, append(base, opts...)...)
}

// ---- Create ---------------------------------------------------------------

func TestCreate(t *testing.T) {
	owner := uuid.New()
	other := uuid.New()
	chID := uuid.New()

	newRepo := func() *fakeRepo {
		r := newFakeRepo()
		r.channels[chID] = sqlcgen.Channel{ID: chID, OwnerID: owner}
		return r
	}

	t.Run("happy path", func(t *testing.T) {
		repo := newRepo()
		svc := enabledService(repo, &fakeDrafter{}, &fakeEnqueuer{}, &fakeLister{})
		sync, err := svc.Create(context.Background(), owner, chID, "https://youtube.com/@chan")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if sync.State != "waiting_first_run" || sync.ChannelID != chID || sync.UserID != owner {
			t.Fatalf("sync = %+v", sync)
		}
	})

	t.Run("disabled → ErrDisabled", func(t *testing.T) {
		repo := newRepo()
		svc := NewService(repo, &fakeDrafter{}, &fakeEnqueuer{}, WithEnabled(false))
		if _, err := svc.Create(context.Background(), owner, chID, "https://youtube.com/@chan"); !errors.Is(err, ErrDisabled) {
			t.Fatalf("err = %v, want ErrDisabled", err)
		}
	})

	t.Run("private URL → ErrInvalidURL", func(t *testing.T) {
		repo := newRepo()
		svc := enabledService(repo, &fakeDrafter{}, &fakeEnqueuer{}, &fakeLister{})
		if _, err := svc.Create(context.Background(), owner, chID, "http://127.0.0.1/@chan"); !errors.Is(err, ErrInvalidURL) {
			t.Fatalf("err = %v, want ErrInvalidURL", err)
		}
	})

	t.Run("non-http URL → ErrInvalidURL", func(t *testing.T) {
		repo := newRepo()
		svc := enabledService(repo, &fakeDrafter{}, &fakeEnqueuer{}, &fakeLister{})
		if _, err := svc.Create(context.Background(), owner, chID, "ftp://example.com/x"); !errors.Is(err, ErrInvalidURL) {
			t.Fatalf("err = %v, want ErrInvalidURL", err)
		}
	})

	t.Run("unknown channel → ErrChannelNotFound", func(t *testing.T) {
		repo := newRepo()
		svc := enabledService(repo, &fakeDrafter{}, &fakeEnqueuer{}, &fakeLister{})
		if _, err := svc.Create(context.Background(), owner, uuid.New(), "https://youtube.com/@chan"); !errors.Is(err, ErrChannelNotFound) {
			t.Fatalf("err = %v, want ErrChannelNotFound", err)
		}
	})

	t.Run("not owner → ErrForbidden", func(t *testing.T) {
		repo := newRepo()
		svc := enabledService(repo, &fakeDrafter{}, &fakeEnqueuer{}, &fakeLister{})
		if _, err := svc.Create(context.Background(), other, chID, "https://youtube.com/@chan"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("cap reached → ErrMaxReached", func(t *testing.T) {
		repo := newRepo()
		svc := enabledService(repo, &fakeDrafter{}, &fakeEnqueuer{}, &fakeLister{}, WithMaxPerUser(2))
		for i := 0; i < 2; i++ {
			if _, err := svc.Create(context.Background(), owner, chID, "https://youtube.com/@chan"+string(rune('a'+i))); err != nil {
				t.Fatalf("seed create %d: %v", i, err)
			}
		}
		if _, err := svc.Create(context.Background(), owner, chID, "https://youtube.com/@third"); !errors.Is(err, ErrMaxReached) {
			t.Fatalf("err = %v, want ErrMaxReached", err)
		}
	})

	t.Run("cap disabled when 0", func(t *testing.T) {
		repo := newRepo()
		svc := enabledService(repo, &fakeDrafter{}, &fakeEnqueuer{}, &fakeLister{}, WithMaxPerUser(0))
		for i := 0; i < 8; i++ {
			if _, err := svc.Create(context.Background(), owner, chID, "https://youtube.com/@chan"+string(rune('a'+i))); err != nil {
				t.Fatalf("create %d past a 0 cap: %v", i, err)
			}
		}
	})

	t.Run("duplicate → ErrConflict", func(t *testing.T) {
		repo := newRepo()
		svc := enabledService(repo, &fakeDrafter{}, &fakeEnqueuer{}, &fakeLister{})
		if _, err := svc.Create(context.Background(), owner, chID, "https://youtube.com/@chan"); err != nil {
			t.Fatalf("first create: %v", err)
		}
		if _, err := svc.Create(context.Background(), owner, chID, "https://youtube.com/@chan"); !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})
}

// ---- Delete / SyncNow -----------------------------------------------------

func TestDeleteAndSyncNow(t *testing.T) {
	owner := uuid.New()
	other := uuid.New()
	repo := newFakeRepo()
	sync := sqlcgen.ChannelSync{ID: uuid.New(), UserID: owner, ChannelID: uuid.New(), State: "idle"}
	repo.syncs[sync.ID] = sync
	svc := enabledService(repo, &fakeDrafter{}, &fakeEnqueuer{}, &fakeLister{})

	t.Run("delete unknown → ErrNotFound", func(t *testing.T) {
		if err := svc.Delete(context.Background(), owner, uuid.New()); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("delete not owner → ErrForbidden", func(t *testing.T) {
		if err := svc.Delete(context.Background(), other, sync.ID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})
	t.Run("sync-now not owner → ErrForbidden", func(t *testing.T) {
		if err := svc.SyncNow(context.Background(), other, sync.ID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
	})
	t.Run("sync-now owner triggers", func(t *testing.T) {
		if err := svc.SyncNow(context.Background(), owner, sync.ID); err != nil {
			t.Fatalf("SyncNow: %v", err)
		}
		if len(repo.triggered) != 1 || repo.triggered[0] != sync.ID {
			t.Fatalf("triggered = %v", repo.triggered)
		}
	})
	t.Run("sync-now disabled → ErrDisabled", func(t *testing.T) {
		off := NewService(repo, &fakeDrafter{}, &fakeEnqueuer{}, WithEnabled(false))
		if err := off.SyncNow(context.Background(), owner, sync.ID); !errors.Is(err, ErrDisabled) {
			t.Fatalf("err = %v, want ErrDisabled", err)
		}
	})
	t.Run("delete owner removes", func(t *testing.T) {
		if err := svc.Delete(context.Background(), owner, sync.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, ok := repo.syncs[sync.ID]; ok {
			t.Fatal("sync still present after delete")
		}
	})
}

// ---- DrainDue -------------------------------------------------------------

func claimRow(s sqlcgen.ChannelSync) sqlcgen.ClaimDueChannelSyncsRow {
	return sqlcgen.ClaimDueChannelSyncsRow{ID: s.ID, ChannelID: s.ChannelID, UserID: s.UserID, ExternalChannelUrl: s.ExternalChannelUrl}
}

func TestDrainDueImportsUnseenEntries(t *testing.T) {
	repo := newFakeRepo()
	sync := sqlcgen.ChannelSync{ID: uuid.New(), UserID: uuid.New(), ChannelID: uuid.New(), ExternalChannelUrl: "https://youtube.com/@chan"}
	repo.syncs[sync.ID] = sync
	repo.claimed = []sqlcgen.ClaimDueChannelSyncsRow{claimRow(sync)}
	// "v1" is already in the ledger; only "v2" should be imported.
	repo.seen[sync.ID.String()+"|v1"] = true

	drafter := &fakeDrafter{}
	enq := &fakeEnqueuer{}
	lister := &fakeLister{entries: []ytdlp.PlaylistEntry{
		{ExternalID: "v1", URL: "https://youtube.com/watch?v=v1", Title: "One"},
		{ExternalID: "v2", URL: "https://youtube.com/watch?v=v2", Title: "Two"},
	}}
	svc := enabledService(repo, drafter, enq, lister)

	done, err := svc.DrainDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainDue: %v", err)
	}
	if done != 1 {
		t.Fatalf("done = %d, want 1", done)
	}
	if lister.gotURL != "https://youtube.com/@chan" || lister.gotN != 15 {
		t.Fatalf("lister called with (%q, %d)", lister.gotURL, lister.gotN)
	}
	// Exactly the unseen entry was drafted + enqueued.
	if len(drafter.calls) != 1 || drafter.calls[0].channelID != sync.ChannelID {
		t.Fatalf("draft calls = %+v", drafter.calls)
	}
	if drafter.calls[0].in.Privacy != syncedDraftPrivacy {
		t.Errorf("draft privacy = %q, want %q", drafter.calls[0].in.Privacy, syncedDraftPrivacy)
	}
	if drafter.calls[0].in.Title != "Two" {
		t.Errorf("draft title = %q, want Two", drafter.calls[0].in.Title)
	}
	if len(enq.calls) != 1 {
		t.Fatalf("enqueue calls = %+v", enq.calls)
	}
	if enq.calls[0].resolver != videoimport.ResolverYtdlp {
		t.Errorf("enqueue resolver = %q, want ytdlp", enq.calls[0].resolver)
	}
	if enq.calls[0].url != "https://youtube.com/watch?v=v2" {
		t.Errorf("enqueue url = %q", enq.calls[0].url)
	}
	// Success reschedules via Finish, never Fail.
	if len(repo.finished) != 1 || len(repo.failed) != 0 {
		t.Fatalf("finished=%d failed=%d", len(repo.finished), len(repo.failed))
	}
	if !repo.finished[0].NextRunAt.After(time.Now()) {
		t.Errorf("next run not scheduled in the future: %v", repo.finished[0].NextRunAt)
	}
}

func TestDrainDueListerFailureRecordsSafeError(t *testing.T) {
	repo := newFakeRepo()
	sync := sqlcgen.ChannelSync{ID: uuid.New(), UserID: uuid.New(), ChannelID: uuid.New(), ExternalChannelUrl: "https://youtube.com/@chan"}
	repo.claimed = []sqlcgen.ClaimDueChannelSyncsRow{claimRow(sync)}
	lister := &fakeLister{err: errors.New("yt-dlp: extractor run failed at https://youtube.com/@chan secret")}
	svc := enabledService(repo, &fakeDrafter{}, &fakeEnqueuer{}, lister)

	done, err := svc.DrainDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainDue: %v", err)
	}
	if done != 0 {
		t.Fatalf("done = %d, want 0", done)
	}
	if len(repo.failed) != 1 {
		t.Fatalf("failed = %d, want 1", len(repo.failed))
	}
	// The stored error is the SAFE reason — never the raw lister error / URL.
	if got := repo.failed[0].LastError; got != "could not list the external channel" {
		t.Fatalf("last_error = %q, want the safe message", got)
	}
}

func TestDrainDuePerEntryErrorsDoNotFailSync(t *testing.T) {
	repo := newFakeRepo()
	sync := sqlcgen.ChannelSync{ID: uuid.New(), UserID: uuid.New(), ChannelID: uuid.New(), ExternalChannelUrl: "https://youtube.com/@chan"}
	repo.claimed = []sqlcgen.ClaimDueChannelSyncsRow{claimRow(sync)}
	lister := &fakeLister{entries: []ytdlp.PlaylistEntry{
		{ExternalID: "v1", URL: "https://youtube.com/watch?v=v1", Title: "One"},
	}}
	// Enqueue always fails (e.g. quota) — the sync must still finish healthy.
	enq := &fakeEnqueuer{err: errors.New("quota exceeded")}
	svc := enabledService(repo, &fakeDrafter{}, enq, lister)

	done, err := svc.DrainDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainDue: %v", err)
	}
	if done != 1 {
		t.Fatalf("done = %d, want 1 (per-entry failure does not fail the sync)", done)
	}
	if len(repo.finished) != 1 || len(repo.failed) != 0 {
		t.Fatalf("finished=%d failed=%d", len(repo.finished), len(repo.failed))
	}
}

func TestDrainDueDisabledIsNoop(t *testing.T) {
	repo := newFakeRepo()
	repo.claimed = []sqlcgen.ClaimDueChannelSyncsRow{{ID: uuid.New()}}
	svc := NewService(repo, &fakeDrafter{}, &fakeEnqueuer{}, WithEnabled(false))
	done, err := svc.DrainDue(context.Background(), 10)
	if err != nil || done != 0 {
		t.Fatalf("DrainDue disabled = (%d, %v), want (0, nil)", done, err)
	}
	if len(repo.finished)+len(repo.failed) != 0 {
		t.Fatal("a disabled drain must not touch any sync")
	}
}

func TestDrainDueMissingListerFailsSafely(t *testing.T) {
	repo := newFakeRepo()
	sync := sqlcgen.ChannelSync{ID: uuid.New(), UserID: uuid.New(), ChannelID: uuid.New(), ExternalChannelUrl: "https://youtube.com/@chan"}
	repo.claimed = []sqlcgen.ClaimDueChannelSyncsRow{claimRow(sync)}
	// Enabled but no lister wired.
	svc := NewService(repo, &fakeDrafter{}, &fakeEnqueuer{}, WithEnabled(true))
	if _, err := svc.DrainDue(context.Background(), 10); err != nil {
		t.Fatalf("DrainDue: %v", err)
	}
	if len(repo.failed) != 1 || repo.failed[0].LastError != "channel sync is not available" {
		t.Fatalf("failed = %+v, want the safe unavailable message", repo.failed)
	}
}
