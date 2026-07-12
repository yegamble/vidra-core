package quota

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func i64(n int64) *int64 { return &n }

func TestEffectiveResolution(t *testing.T) {
	cases := []struct {
		name            string
		override        *int64
		instanceDefault int64
		want            *int64 // nil = unlimited
	}{
		{"no override, unlimited instance", nil, 0, nil},
		{"no override, instance default applies", nil, 100, i64(100)},
		{"override wins over instance default", i64(50), 100, i64(50)},
		{"override 0 = unlimited even with a finite default", i64(0), 100, nil},
		{"override applies when instance is unlimited", i64(50), 0, i64(50)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Effective(tc.override, tc.instanceDefault)
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("Effective(%v, %d) = %d, want unlimited (nil)", tc.override, tc.instanceDefault, *got)
			case tc.want != nil && (got == nil || *got != *tc.want):
				t.Errorf("Effective(%v, %d) = %v, want %d", tc.override, tc.instanceDefault, got, *tc.want)
			}
		})
	}
}

// usageEvent is one recorded daily-ledger row (fakeRepo).
type usageEvent struct {
	userID    uuid.UUID
	bytes     int64
	createdAt time.Time
}

// fakeRepo is an in-memory quota.Repository.
type fakeRepo struct {
	users  map[uuid.UUID]sqlcgen.User
	used   map[uuid.UUID]int64
	events []usageEvent
	// eventClock stamps recorded events (RecordUploadUsageEvent has no
	// timestamp parameter — the DB defaults created_at — so the fake needs its
	// own stamp source).
	eventClock func() time.Time
}

func (f *fakeRepo) GetUserByID(_ context.Context, id uuid.UUID) (sqlcgen.User, error) {
	u, ok := f.users[id]
	if !ok {
		return sqlcgen.User{}, errors.New("not found")
	}
	return u, nil
}

func (f *fakeRepo) SumUserStorageUsage(_ context.Context, ownerID uuid.UUID) (int64, error) {
	return f.used[ownerID], nil
}

func (f *fakeRepo) RecordUploadUsageEvent(_ context.Context, arg sqlcgen.RecordUploadUsageEventParams) error {
	now := time.Now()
	if f.eventClock != nil {
		now = f.eventClock()
	}
	f.events = append(f.events, usageEvent{userID: arg.UserID, bytes: arg.Bytes, createdAt: now})
	return nil
}

func (f *fakeRepo) SumUploadUsageSince(_ context.Context, arg sqlcgen.SumUploadUsageSinceParams) (int64, error) {
	var sum int64
	for _, e := range f.events {
		if e.userID == arg.UserID && e.createdAt.After(arg.CreatedAt) {
			sum += e.bytes
		}
	}
	return sum, nil
}

func (f *fakeRepo) PruneUploadUsageEvents(_ context.Context, arg sqlcgen.PruneUploadUsageEventsParams) (int64, error) {
	kept := f.events[:0]
	var n int64
	for _, e := range f.events {
		if e.userID == arg.UserID && e.createdAt.Before(arg.CreatedAt) {
			n++
			continue
		}
		kept = append(kept, e)
	}
	f.events = kept
	return n, nil
}

func seed(override *int64, used int64) (*fakeRepo, uuid.UUID) {
	id := uuid.New()
	return &fakeRepo{
		users: map[uuid.UUID]sqlcgen.User{id: {ID: id, StorageQuotaBytes: override}},
		used:  map[uuid.UUID]int64{id: used},
	}, id
}

// TestDefaultBytesFuncOverlay proves WithDefaultBytesFunc supersedes the
// constructor default and is resolved live per Status (the instance-settings
// overlay), while a per-user override still wins over it.
func TestDefaultBytesFuncOverlay(t *testing.T) {
	ctx := context.Background()
	repo, id := seed(nil, 10) // no per-user override → the instance default applies

	current := int64(100)
	svc := NewService(repo, 999, WithDefaultBytesFunc(func() int64 { return current }))

	// The func supersedes the constructor's 999.
	if st, _ := svc.Status(ctx, id); st.QuotaBytes == nil || *st.QuotaBytes != 100 {
		t.Fatalf("Status.QuotaBytes = %v, want 100 (from the func, not 999)", st.QuotaBytes)
	}
	// It is resolved live: a later value takes effect with no reconstruction.
	current = 50
	if st, _ := svc.Status(ctx, id); st.QuotaBytes == nil || *st.QuotaBytes != 50 {
		t.Fatalf("Status.QuotaBytes after change = %v, want 50", st.QuotaBytes)
	}
	// 0 from the func means unlimited.
	current = 0
	if st, _ := svc.Status(ctx, id); st.QuotaBytes != nil {
		t.Errorf("func 0 should be unlimited, got %d", *st.QuotaBytes)
	}

	// A per-user override still wins over the instance-default func.
	repoOv, idOv := seed(i64(70), 10)
	svcOv := NewService(repoOv, 999, WithDefaultBytesFunc(func() int64 { return 500 }))
	if st, _ := svcOv.Status(ctx, idOv); st.QuotaBytes == nil || *st.QuotaBytes != 70 {
		t.Fatalf("per-user override Status.QuotaBytes = %v, want 70", st.QuotaBytes)
	}
}

func TestStatusAndRemaining(t *testing.T) {
	ctx := context.Background()

	// Limited by the instance default.
	repo, id := seed(nil, 30)
	svc := NewService(repo, 100)
	st, err := svc.Status(ctx, id)
	if err != nil || st.UsedBytes != 30 || st.QuotaBytes == nil || *st.QuotaBytes != 100 {
		t.Fatalf("Status = (%+v, %v), want used=30 quota=100", st, err)
	}
	rem, limited, err := svc.Remaining(ctx, id)
	if err != nil || !limited || rem != 70 {
		t.Fatalf("Remaining = (%d, %v, %v), want (70, true, nil)", rem, limited, err)
	}

	// Unlimited: no cap reported.
	repo, id = seed(nil, 30)
	svc = NewService(repo, 0)
	st, _ = svc.Status(ctx, id)
	if st.QuotaBytes != nil {
		t.Errorf("unlimited Status.QuotaBytes = %d, want nil", *st.QuotaBytes)
	}
	if _, limited, _ := svc.Remaining(ctx, id); limited {
		t.Errorf("unlimited Remaining reported limited=true")
	}

	// Already over quota (e.g. the admin lowered it): remaining clamps at 0.
	repo, id = seed(i64(10), 25)
	svc = NewService(repo, 0)
	rem, limited, _ = svc.Remaining(ctx, id)
	if !limited || rem != 0 {
		t.Errorf("over-quota Remaining = (%d, %v), want (0, true)", rem, limited)
	}

	// Unknown user surfaces the repo error.
	if _, err := svc.Status(ctx, uuid.New()); err == nil {
		t.Errorf("Status(unknown) = nil error, want error")
	}
}

// TestDailyRollingWindow drives the rolling-24h math with a fake clock: only
// events inside the trailing window count, the boundary is exact, and 0 means
// unlimited (ledger not consulted).
func TestDailyRollingWindow(t *testing.T) {
	ctx := context.Background()
	repo, id := seed(nil, 0)

	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	repo.eventClock = clock

	daily := int64(100)
	svc := NewService(repo, 0,
		WithDailyBytesFunc(func() int64 { return daily }),
		WithClock(clock))

	// Record 40 bytes now-25h (outside), 30 bytes now-23h (inside), 20 now.
	now = now.Add(-25 * time.Hour)
	if err := svc.RecordUpload(ctx, id, 40); err != nil {
		t.Fatalf("RecordUpload: %v", err)
	}
	now = now.Add(2 * time.Hour) // now-23h
	if err := svc.RecordUpload(ctx, id, 30); err != nil {
		t.Fatalf("RecordUpload: %v", err)
	}
	now = now.Add(23 * time.Hour) // back to the original "now"
	if err := svc.RecordUpload(ctx, id, 20); err != nil {
		t.Fatalf("RecordUpload: %v", err)
	}

	st, err := svc.Daily(ctx, id)
	if err != nil || st.QuotaBytes == nil || *st.QuotaBytes != 100 || st.UsedBytes != 50 {
		t.Fatalf("Daily = (%+v, %v), want used=50 quota=100 (25h-old event ignored)", st, err)
	}

	// Boundary: exactly-remaining fits, one more byte does not.
	if err := svc.CheckDailyFits(ctx, id, 50); err != nil {
		t.Errorf("exactly-at-limit CheckDailyFits = %v, want nil", err)
	}
	if err := svc.CheckDailyFits(ctx, id, 51); !errors.Is(err, ErrDailyExceeded) {
		t.Errorf("over-limit CheckDailyFits = %v, want ErrDailyExceeded", err)
	}

	// The window ROLLS: 2h later the 23h-old event ages out too.
	now = now.Add(2 * time.Hour)
	if st, _ := svc.Daily(ctx, id); st.UsedBytes != 20 {
		t.Errorf("Daily.UsedBytes after 2h = %d, want 20 (30-byte event aged out)", st.UsedBytes)
	}

	// 0 = unlimited: the cap disappears and nothing is enforced.
	daily = 0
	if st, _ := svc.Daily(ctx, id); st.QuotaBytes != nil || st.UsedBytes != 0 {
		t.Errorf("unlimited Daily = %+v, want zero-value status", st)
	}
	if err := svc.CheckDailyFits(ctx, id, 1<<40); err != nil {
		t.Errorf("unlimited CheckDailyFits = %v, want nil", err)
	}
}

// TestDailyUnlimitedWithoutProvider proves the plain constructor (no daily
// provider) enforces nothing and never queries the ledger.
func TestDailyUnlimitedWithoutProvider(t *testing.T) {
	ctx := context.Background()
	repo, id := seed(nil, 0)
	svc := NewService(repo, 0)
	if err := svc.RecordUpload(ctx, id, 10); err != nil {
		t.Fatalf("RecordUpload: %v", err)
	}
	if st, err := svc.Daily(ctx, id); err != nil || st.QuotaBytes != nil || st.UsedBytes != 0 {
		t.Fatalf("Daily without provider = (%+v, %v), want zero-value", st, err)
	}
	if err := svc.CheckDailyFits(ctx, id, 1<<40); err != nil {
		t.Fatalf("CheckDailyFits without provider = %v, want nil", err)
	}
}

// TestRecordUploadPrunesOldEvents proves record-time pruning keeps the ledger
// bounded (retention = 2x window) and ignores non-positive sizes.
func TestRecordUploadPrunesOldEvents(t *testing.T) {
	ctx := context.Background()
	repo, id := seed(nil, 0)
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	repo.eventClock = clock
	svc := NewService(repo, 0, WithClock(clock),
		WithDailyBytesFunc(func() int64 { return 100 }))

	if err := svc.RecordUpload(ctx, id, 10); err != nil {
		t.Fatalf("RecordUpload: %v", err)
	}
	// 3 days later a new record prunes the stale event (older than 48h).
	now = now.Add(72 * time.Hour)
	if err := svc.RecordUpload(ctx, id, 20); err != nil {
		t.Fatalf("RecordUpload: %v", err)
	}
	if len(repo.events) != 1 || repo.events[0].bytes != 20 {
		t.Fatalf("ledger after prune = %+v, want only the fresh 20-byte event", repo.events)
	}
	// Zero/negative bytes are ignored (no event row).
	if err := svc.RecordUpload(ctx, id, 0); err != nil {
		t.Fatalf("RecordUpload(0): %v", err)
	}
	if len(repo.events) != 1 {
		t.Fatalf("zero-byte record appended an event")
	}
}

func TestCheckFits(t *testing.T) {
	ctx := context.Background()
	repo, id := seed(i64(100), 90)
	svc := NewService(repo, 0)

	if err := svc.CheckFits(ctx, id, 10); err != nil {
		t.Errorf("exactly-at-limit CheckFits = %v, want nil", err)
	}
	if err := svc.CheckFits(ctx, id, 11); !errors.Is(err, ErrExceeded) {
		t.Errorf("over-limit CheckFits = %v, want ErrExceeded", err)
	}

	// Unlimited never rejects.
	repo, id = seed(nil, 1<<40)
	svc = NewService(repo, 0)
	if err := svc.CheckFits(ctx, id, 1<<40); err != nil {
		t.Errorf("unlimited CheckFits = %v, want nil", err)
	}
}
