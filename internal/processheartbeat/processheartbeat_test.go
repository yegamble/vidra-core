package processheartbeat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo mirrors the SQL semantics that matter: an upsert keyed on process_id,
// a stopped flag, and a list that hides rows older than the forget window.
type fakeRepo struct {
	rows    map[string]sqlcgen.ProcessHeartbeat
	now     time.Time
	listErr error
	forgot  int64
}

func newFakeRepo(now time.Time) *fakeRepo {
	return &fakeRepo{rows: map[string]sqlcgen.ProcessHeartbeat{}, now: now}
}

func (f *fakeRepo) UpsertProcessHeartbeat(_ context.Context, arg sqlcgen.UpsertProcessHeartbeatParams) error {
	f.rows[arg.ProcessID] = sqlcgen.ProcessHeartbeat{
		ProcessID:                 arg.ProcessID,
		Role:                      arg.Role,
		Hostname:                  arg.Hostname,
		Pid:                       arg.Pid,
		Version:                   arg.Version,
		BuildCommit:               arg.BuildCommit,
		State:                     "running",
		StartedAt:                 arg.StartedAt,
		LastSeenAt:                f.now,
		LastSettingsPollSuccessAt: arg.LastSettingsPollSuccessAt,
		LastSettingsPollError:     arg.LastSettingsPollError,
	}
	return nil
}

func (f *fakeRepo) MarkProcessStopped(_ context.Context, id string) error {
	r, ok := f.rows[id]
	if !ok {
		return nil
	}
	r.State = "stopped"
	r.LastSeenAt = f.now
	f.rows[id] = r
	return nil
}

func (f *fakeRepo) ListProcessHeartbeats(_ context.Context, forgetAfter pgtype.Interval) ([]sqlcgen.ProcessHeartbeat, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	cutoff := f.now.Add(-time.Duration(forgetAfter.Microseconds) * time.Microsecond)
	var out []sqlcgen.ProcessHeartbeat
	for _, r := range f.rows {
		if r.LastSeenAt.Before(cutoff) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeRepo) ForgetStaleProcessHeartbeats(_ context.Context, forgetAfter pgtype.Interval) (int64, error) {
	cutoff := f.now.Add(-time.Duration(forgetAfter.Microseconds) * time.Microsecond)
	var n int64
	for id, r := range f.rows {
		if r.LastSeenAt.Before(cutoff) {
			delete(f.rows, id)
			n++
		}
	}
	f.forgot += n
	return n, nil
}

type fakeHealth struct {
	last time.Time
	err  error
}

func (f fakeHealth) Health() (time.Time, error) { return f.last, f.err }

func TestProcessIDIsHostAndPID(t *testing.T) {
	if got := ProcessID("vidra-worker-1", 1); got != "vidra-worker-1:1" {
		t.Fatalf("ProcessID = %q", got)
	}
	// A host with no name must still produce a usable key rather than ":1",
	// which would collide across every anonymous host in a fleet.
	if got := ProcessID("", 42); got != "unknown-host:42" {
		t.Fatalf("ProcessID with no hostname = %q", got)
	}
}

func TestWriterUpsertsAndCarriesPollHealth(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	repo := newFakeRepo(now)
	last := now.Add(-4 * time.Second)
	w := NewWriter(repo, fakeHealth{last: last}, Config{
		Role: "worker", Version: "0.6.3", Commit: "abc1234",
		Hostname: "box", PID: 77, StartedAt: now.Add(-time.Minute),
	})
	if err := w.Beat(context.Background()); err != nil {
		t.Fatalf("Beat: %v", err)
	}
	row, ok := repo.rows["box:77"]
	if !ok {
		t.Fatalf("no row written; have %v", repo.rows)
	}
	if row.Role != "worker" || row.Version != "0.6.3" || row.BuildCommit != "abc1234" {
		t.Errorf("row identity = %+v", row)
	}
	if !row.LastSettingsPollSuccessAt.Valid || !row.LastSettingsPollSuccessAt.Time.Equal(last) {
		t.Errorf("poll success = %+v, want %v", row.LastSettingsPollSuccessAt, last)
	}
	if row.LastSettingsPollError != "" {
		t.Errorf("poll error = %q, want empty on a healthy poll", row.LastSettingsPollError)
	}

	// A failing poll must travel with the heartbeat: the tick that FAILED is the
	// one the admin page most needs to hear about, so recording only successes
	// would leave a wedged replica indistinguishable from a healthy one.
	w.health = fakeHealth{last: last, err: errors.New("relation \"settings_version\" does not exist")}
	if err := w.Beat(context.Background()); err != nil {
		t.Fatalf("Beat after failure: %v", err)
	}
	if got := repo.rows["box:77"].LastSettingsPollError; got == "" {
		t.Fatal("a failing poll left no error on the heartbeat row")
	}
	if got := repo.rows["box:77"].LastSettingsPollSuccessAt; !got.Valid || !got.Time.Equal(last) {
		t.Errorf("last success = %+v; a failure must keep the last TRUE success, which is what bounds the staleness", got)
	}
}

func TestWriterStopSaysGoodbye(t *testing.T) {
	now := time.Now().UTC()
	repo := newFakeRepo(now)
	w := NewWriter(repo, nil, Config{Role: "api", Hostname: "box", PID: 5})
	if err := w.Beat(context.Background()); err != nil {
		t.Fatalf("Beat: %v", err)
	}
	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := repo.rows["box:5"].State; got != "stopped" {
		t.Fatalf("state after Stop = %q, want stopped", got)
	}
	// And a stopped process must NOT degrade the instance: that is what a
	// scale-down looks like.
	f := NewFleet(repo, "other:1", 10*time.Second, time.Hour)
	rows, err := f.List(context.Background(), now)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, degraded := Judge(rows); degraded {
		t.Fatal("a cleanly stopped process degraded the fleet")
	}
}

func TestNilWriterAndNilFleetAreInert(t *testing.T) {
	var w *Writer
	if err := w.Beat(context.Background()); err != nil {
		t.Errorf("nil Beat: %v", err)
	}
	if err := w.Stop(context.Background()); err != nil {
		t.Errorf("nil Stop: %v", err)
	}
	if got := w.ProcessID(); got != "" {
		t.Errorf("nil ProcessID = %q", got)
	}
	w.AfterTick(context.Background(), nil)
	if NewWriter(nil, nil, Config{}) != nil {
		t.Error("NewWriter with no repo must be nil so unwired processes stay unwired")
	}
	var f *Fleet
	rows, err := f.List(context.Background(), time.Now())
	if err != nil || rows != nil {
		t.Errorf("nil List = %v, %v", rows, err)
	}
	if NewFleet(nil, "", 0, 0) != nil {
		t.Error("NewFleet with no repo must be nil so the page omits the list")
	}
}

func TestFleetStalenessAndJudgement(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	interval := 10 * time.Second
	repo := newFakeRepo(now)
	f := NewFleet(repo, "box:1", interval, 24*time.Hour)
	if got, want := f.StaleWindow(), 30*time.Second; got != want {
		t.Fatalf("StaleWindow = %v, want %v (three intervals)", got, want)
	}

	fresh := func(id, role string, seenAgo time.Duration, pollErr string) {
		repo.rows[id] = sqlcgen.ProcessHeartbeat{
			ProcessID: id, Role: role, State: "running",
			StartedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-seenAgo),
			LastSettingsPollSuccessAt: pgtype.Timestamptz{Time: now.Add(-seenAgo), Valid: true},
			LastSettingsPollError:     pollErr,
		}
	}

	// Both healthy: no fault, and the api knows which row is itself.
	fresh("box:1", "api", 2*time.Second, "")
	fresh("box:2", "worker", 3*time.Second, "")
	rows, err := f.List(context.Background(), now)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	var sawSelf bool
	for _, r := range rows {
		if r.State != "running" || r.SettingsPoll != "ok" {
			t.Errorf("%s = %s/%s, want running/ok", r.ProcessID, r.State, r.SettingsPoll)
		}
		if r.Self {
			sawSelf = true
			if r.ProcessID != "box:1" {
				t.Errorf("self = %s", r.ProcessID)
			}
		}
	}
	if !sawSelf {
		t.Error("no row marked self; an operator cannot tell which process answered")
	}
	if fault, degraded := Judge(rows); degraded {
		t.Fatalf("healthy fleet degraded: %s", fault.Reason)
	}

	// One tick missed is noise, not an absence.
	fresh("box:2", "worker", 12*time.Second, "")
	rows, _ = f.List(context.Background(), now)
	if fault, degraded := Judge(rows); degraded {
		t.Fatalf("one missed tick degraded the fleet: %s", fault.Reason)
	}

	// Past three intervals it is an absence, and the message must NAME it —
	// a degraded page that does not say which process is gone sends the
	// operator to the wrong machine.
	fresh("box:2", "worker", 47*time.Second, "")
	rows, _ = f.List(context.Background(), now)
	fault, degraded := Judge(rows)
	if !degraded {
		t.Fatal("a worker silent for 47s did not degrade the fleet")
	}
	if fault.Process.ProcessID != "box:2" || fault.Process.State != "stale" {
		t.Errorf("fault process = %+v", fault.Process)
	}
	for _, want := range []string{"box:2", "worker", "47s"} {
		if !strings.Contains(fault.Reason, want) {
			t.Errorf("reason %q does not name %q", fault.Reason, want)
		}
	}

	// A worker that is present but cannot poll is a different, still-degraded
	// fact — and the api's own health being fine must not hide it.
	fresh("box:2", "worker", 3*time.Second, "connection refused")
	rows, _ = f.List(context.Background(), now)
	fault, degraded = Judge(rows)
	if !degraded {
		t.Fatal("a worker whose poll is failing did not degrade the fleet")
	}
	if fault.Process.ProcessID != "box:2" || fault.Process.SettingsPoll != "failing" {
		t.Errorf("fault process = %+v", fault.Process)
	}
	for _, want := range []string{"box:2", "worker", "connection refused", "stale instance settings"} {
		if !strings.Contains(fault.Reason, want) {
			t.Errorf("reason %q does not name %q", fault.Reason, want)
		}
	}

	// Absence beats a reported failure: a process still reporting is at least
	// there to fix itself.
	fresh("box:2", "worker", 3*time.Second, "connection refused")
	fresh("box:3", "worker", 90*time.Second, "")
	rows, _ = f.List(context.Background(), now)
	fault, _ = Judge(rows)
	if fault.Process.ProcessID != "box:3" {
		t.Errorf("fault = %s, want the silent process box:3 to win over the failing one", fault.Process.ProcessID)
	}
}

func TestFleetForgetsAndHidesAncientRows(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	repo := newFakeRepo(now)
	repo.rows["gone:1"] = sqlcgen.ProcessHeartbeat{
		ProcessID: "gone:1", Role: "worker", State: "running",
		StartedAt: now.Add(-100 * time.Hour), LastSeenAt: now.Add(-72 * time.Hour),
	}
	f := NewFleet(repo, "box:1", 10*time.Second, 24*time.Hour)
	rows, err := f.List(context.Background(), now)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("a replica gone for three days is still in the fleet: %+v", rows)
	}
	// ...and the sweep removes it, so the table stays the size of the fleet.
	w := NewWriter(repo, nil, Config{Role: "api", Hostname: "box", PID: 1, ForgetAfter: 24 * time.Hour})
	n, err := w.Forget(context.Background())
	if err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if n != 1 {
		t.Fatalf("Forget removed %d rows, want 1", n)
	}
}

func TestForgetSweepIsRateLimited(t *testing.T) {
	now := time.Now().UTC()
	w := NewWriter(newFakeRepo(now), nil, Config{Role: "api", Hostname: "box", PID: 1})
	if !w.dueForForget(now) {
		t.Fatal("the first tick must sweep")
	}
	if w.dueForForget(now.Add(time.Minute)) {
		t.Fatal("a sweep a minute later would put a DELETE on every tick of every process")
	}
	if !w.dueForForget(now.Add(2 * time.Hour)) {
		t.Fatal("the sweep never came back")
	}
}
