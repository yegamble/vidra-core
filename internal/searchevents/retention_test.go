package searchevents

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakePruneRepo mirrors the SQL's semantics, including the guard that makes
// state='pending' unmatchable: the query carries `AND state <> 'pending'`, so a
// caller that passed 'pending' would delete nothing. The fake reproduces that
// rather than trusting the caller, so a test can distinguish "the pruner never
// asks" (asserted separately) from "asking would have been harmless".
type fakePruneRepo struct {
	calls     []sqlcgen.PruneSearchOutboxParams
	remaining map[string]int64
	err       error
}

func newFakePruneRepo() *fakePruneRepo {
	return &fakePruneRepo{remaining: map[string]int64{}}
}

func (f *fakePruneRepo) PruneSearchOutbox(_ context.Context, a sqlcgen.PruneSearchOutboxParams) (int64, error) {
	f.calls = append(f.calls, a)
	if f.err != nil {
		return 0, f.err
	}
	if a.State == "pending" {
		return 0, nil
	}
	n := f.remaining[a.State]
	if n > int64(a.BatchSize) {
		n = int64(a.BatchSize)
	}
	f.remaining[a.State] -= n
	return n, nil
}

func (f *fakePruneRepo) statesAsked() []string {
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.State)
	}
	return out
}

// cutoffFor returns the cutoff of the first call for state.
func (f *fakePruneRepo) cutoffFor(t *testing.T, state string) time.Time {
	t.Helper()
	for _, c := range f.calls {
		if c.State == state {
			return c.Cutoff
		}
	}
	t.Fatalf("no prune call for state %q (calls: %v)", state, f.statesAsked())
	return time.Time{}
}

func fixedRetention(days int64) func() int64 { return func() int64 { return days } }

// TestPrunerStateAwareWindows: delivered rows go on the operator's declared
// search-event window; dead rows go on the same window whenever it is at least
// the forensic floor.
func TestPrunerStateAwareWindows(t *testing.T) {
	repo := newFakePruneRepo()
	p := NewPruner(repo, fixedRetention(90), testLogger())
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	delivered, dead, err := p.Prune(context.Background(), now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if delivered != 0 || dead != 0 {
		t.Fatalf("empty table: got delivered=%d dead=%d, want 0/0", delivered, dead)
	}
	if got := repo.cutoffFor(t, StateDelivered); !got.Equal(now.AddDate(0, 0, -90)) {
		t.Errorf("delivered cutoff = %s, want %s", got, now.AddDate(0, 0, -90))
	}
	if got := repo.cutoffFor(t, StateDead); !got.Equal(now.AddDate(0, 0, -90)) {
		t.Errorf("dead cutoff = %s, want %s (same window at the default)", got, now.AddDate(0, 0, -90))
	}
}

// TestPrunerNeverPrunesPending is the data-loss guard: an undelivered row is an
// index mutation or a privacy purge that has not happened yet. The pruner must
// never even ask for that state.
func TestPrunerNeverPrunesPending(t *testing.T) {
	repo := newFakePruneRepo()
	repo.remaining[StatePending] = 500
	p := NewPruner(repo, fixedRetention(1), testLogger())

	if _, _, err := p.Prune(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	for _, s := range repo.statesAsked() {
		if s == StatePending {
			t.Fatalf("pruner asked to delete pending rows (states: %v)", repo.statesAsked())
		}
	}
	if repo.remaining[StatePending] != 500 {
		t.Errorf("pending rows deleted: %d remain of 500", repo.remaining[StatePending])
	}
}

// TestPrunerDeadForensicFloor: a dead row is the only evidence of a delivery
// failure, and the drainer's whole retry lifetime is under a day. On an install
// that tightens retention below the floor, dead rows still survive the floor.
func TestPrunerDeadForensicFloor(t *testing.T) {
	repo := newFakePruneRepo()
	p := NewPruner(repo, fixedRetention(1), testLogger())
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	if _, _, err := p.Prune(context.Background(), now); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if got, want := repo.cutoffFor(t, StateDelivered), now.AddDate(0, 0, -1); !got.Equal(want) {
		t.Errorf("delivered cutoff = %s, want %s", got, want)
	}
	if got, want := repo.cutoffFor(t, StateDead), now.Add(-DeadForensicFloor); !got.Equal(want) {
		t.Errorf("dead cutoff = %s, want the %s floor at %s", got, DeadForensicFloor, want)
	}
}

// TestPrunerRetentionFallback: an unreadable or out-of-range setting must not
// widen the window to infinity (which is today's bug) nor collapse it to zero
// (which would delete rows the operator meant to keep).
func TestPrunerRetentionFallback(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		days func() int64
	}{
		{"no provider", nil},
		{"zero", fixedRetention(0)},
		{"negative", fixedRetention(-5)},
		{"above the validated maximum", fixedRetention(9000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakePruneRepo()
			p := NewPruner(repo, tc.days, testLogger())
			if _, _, err := p.Prune(context.Background(), now); err != nil {
				t.Fatalf("Prune: %v", err)
			}
			want := now.AddDate(0, 0, -DefaultEventRetentionDays)
			if got := repo.cutoffFor(t, StateDelivered); !got.Equal(want) {
				t.Errorf("delivered cutoff = %s, want the %d-day default at %s", got, DefaultEventRetentionDays, want)
			}
		})
	}
}

// TestPrunerBatchesUntilShort: a backlog larger than one batch converges over
// repeated batches within a single sweep, and a sweep with nothing left costs
// exactly one query per state.
func TestPrunerBatchesUntilShort(t *testing.T) {
	repo := newFakePruneRepo()
	repo.remaining[StateDelivered] = int64(pruneBatchSize)*2 + 7
	p := NewPruner(repo, fixedRetention(90), testLogger())

	delivered, _, err := p.Prune(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if want := int64(pruneBatchSize)*2 + 7; delivered != want {
		t.Fatalf("deleted %d, want %d", delivered, want)
	}
	if n := len(repo.calls); n != 4 { // 3 delivered batches (last short) + 1 dead
		t.Errorf("issued %d queries, want 4 (three delivered batches + one dead)", n)
	}
	for _, c := range repo.calls {
		if c.BatchSize != pruneBatchSize {
			t.Errorf("batch size = %d, want %d: an unbounded DELETE locks the queue", c.BatchSize, pruneBatchSize)
		}
	}

	// Re-running is a no-op, not a second deletion.
	repo.calls = nil
	delivered, dead, err := p.Prune(context.Background(), time.Now().UTC())
	if err != nil || delivered != 0 || dead != 0 {
		t.Fatalf("second sweep: delivered=%d dead=%d err=%v, want 0/0/nil", delivered, dead, err)
	}
	if n := len(repo.calls); n != 2 {
		t.Errorf("idle sweep issued %d queries, want 2 (one per prunable state)", n)
	}
}

// TestPrunerBatchCap bounds one sweep so a catch-up on a years-old table cannot
// become an unbounded transaction storm.
func TestPrunerBatchCap(t *testing.T) {
	repo := newFakePruneRepo()
	repo.remaining[StateDelivered] = int64(pruneBatchSize) * int64(pruneMaxBatches+10)
	p := NewPruner(repo, fixedRetention(90), testLogger())

	delivered, _, err := p.Prune(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if want := int64(pruneBatchSize) * int64(pruneMaxBatches); delivered != want {
		t.Errorf("deleted %d in one sweep, want the cap %d", delivered, want)
	}
}

// TestPrunerSurfacesErrors: a failing prune is reported, not silently counted as
// a successful sweep — retention that quietly stops working is the defect this
// worker exists to close.
func TestPrunerSurfacesErrors(t *testing.T) {
	repo := newFakePruneRepo()
	repo.err = errors.New("boom")
	p := NewPruner(repo, fixedRetention(90), testLogger())
	if _, _, err := p.Prune(context.Background(), time.Now().UTC()); err == nil {
		t.Fatal("Prune returned nil error on a failing repository")
	}

	var nilPruner *Pruner
	if _, _, err := nilPruner.Prune(context.Background(), time.Now().UTC()); !errors.Is(err, ErrPrunerUnavailable) {
		t.Errorf("nil pruner: err = %v, want ErrPrunerUnavailable", err)
	}
}
