package jobloop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// The loops this package replaces lived in cmd/api and had NO test coverage of
// any kind — they were exercised only by running the process. These tests are
// the whole net under twenty-odd background workers, so they pin the parts an
// operator would notice going wrong: a tick that stops firing, a shutdown that
// hangs, a follower that starts running singleton sweeps, a drain that stops
// after one batch, and an error that kills the loop instead of being logged.

// tickInterval is short enough to keep the suite fast and long enough that a
// loaded machine still gets several ticks inside the waits below.
const tickInterval = 5 * time.Millisecond

// recorder captures structured log records for assertions.
type recorder struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newRecorder() (*recorder, *slog.Logger) {
	r := &recorder{}
	return r, slog.New(slog.NewJSONHandler(r, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func (r *recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}

// records returns every log line as a decoded map.
func (r *recorder) records() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(r.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

// find returns the first record whose msg matches, or nil.
func (r *recorder) find(msg string) map[string]any {
	for _, rec := range r.records() {
		if rec["msg"] == msg {
			return rec
		}
	}
	return nil
}

// counter is a goroutine-safe call count.
type counter struct {
	mu sync.Mutex
	n  int
}

func (c *counter) inc() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return c.n
}

func (c *counter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// waitFor polls cond until it holds or the deadline passes, so the tests never
// sleep for a fixed duration hoping a tick landed.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Microsecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type fakeLeader struct {
	mu     sync.Mutex
	leader bool
}

func (f *fakeLeader) IsLeader() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.leader
}

func (f *fakeLeader) set(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leader = v
}

// runLoop starts l in a goroutine and returns a stop function that cancels it
// and waits for it to exit — which is also the assertion that cancellation is
// honoured, since a loop that ignored ctx would hang the test.
func runLoop(t *testing.T, l Loop, logger *slog.Logger) (context.Context, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.Run(ctx, logger)
	}()
	return ctx, func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("loop did not return after its context was canceled")
		}
	}
}

func TestTickRunsThePass(t *testing.T) {
	_, logger := newRecorder()
	var calls counter
	_, stop := runLoop(t, Loop{
		Interval: tickInterval,
		Passes: []Pass{{
			Run: func(context.Context, time.Time) (int, error) { calls.inc(); return 0, nil },
		}},
	}, logger)
	defer stop()
	waitFor(t, "the pass to run on successive ticks", func() bool { return calls.get() >= 3 })
}

func TestCancelExitsBeforeTheFirstTick(t *testing.T) {
	_, logger := newRecorder()
	var calls counter
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		Loop{
			Interval: time.Hour,
			Passes: []Pass{{
				Run: func(context.Context, time.Time) (int, error) { calls.inc(); return 0, nil },
			}},
		}.Run(ctx, logger)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a loop started with a canceled context did not return")
	}
	if calls.get() != 0 {
		t.Errorf("pass ran %d times on an already-canceled context, want 0", calls.get())
	}
}

// TestJitterHonoursCancellation is the shutdown path that is easy to get wrong:
// a loop canceled during its start-up jitter must return without ever creating
// a ticker, or a deploy waits out the jitter of every worker.
func TestJitterHonoursCancellation(t *testing.T) {
	_, logger := newRecorder()
	var calls counter
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Loop{
			Interval: 10 * time.Second, // jitter picks somewhere inside this
			Jitter:   true,
			Passes: []Pass{{
				Run: func(context.Context, time.Time) (int, error) { calls.inc(); return 0, nil },
			}},
		}.Run(ctx, logger)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a loop canceled during its start jitter did not return")
	}
	if calls.get() != 0 {
		t.Errorf("pass ran %d times during jitter, want 0", calls.get())
	}
}

// TestLeaderGateSkipsFollowerTicks is the one that protects against a whole
// fleet running a singleton sweep at once. It also checks the gate is consulted
// per tick, not once at start: leadership moves while the process runs.
func TestLeaderGateSkipsFollowerTicks(t *testing.T) {
	_, logger := newRecorder()
	leader := &fakeLeader{leader: false}
	var calls counter
	_, stop := runLoop(t, Loop{
		Interval: tickInterval,
		Leader:   leader,
		Passes: []Pass{{
			Run: func(context.Context, time.Time) (int, error) { calls.inc(); return 0, nil },
		}},
	}, logger)
	defer stop()

	time.Sleep(20 * tickInterval)
	if got := calls.get(); got != 0 {
		t.Fatalf("a follower ran the pass %d times, want 0", got)
	}

	leader.set(true)
	waitFor(t, "the pass to run once leadership is acquired", func() bool { return calls.get() > 0 })

	leader.set(false)
	settled := calls.get()
	time.Sleep(20 * tickInterval)
	if got := calls.get(); got > settled+1 {
		// One extra is tolerated: a tick already inside the pass when the flag
		// flipped still completes.
		t.Errorf("pass ran %d more times after leadership was lost, want it to stop", got-settled)
	}
}

// TestNilLeaderFieldRunsUngated pins the distinction the field carries: an unset
// Leader means "not a singleton", which must not be confused with an elector
// that happens to report false.
func TestNilLeaderFieldRunsUngated(t *testing.T) {
	_, logger := newRecorder()
	var calls counter
	_, stop := runLoop(t, Loop{
		Interval: tickInterval,
		Passes: []Pass{{
			Run: func(context.Context, time.Time) (int, error) { calls.inc(); return 0, nil },
		}},
	}, logger)
	defer stop()
	waitFor(t, "an ungated loop to run", func() bool { return calls.get() > 0 })
}

// TestDrainRepeatsUntilEmpty is what keeps a burst of uploads from waiting one
// tick per batch.
func TestDrainRepeatsUntilEmpty(t *testing.T) {
	rec, logger := newRecorder()
	batches := []int{5, 5, 3, 0}
	var idx counter
	var firstTick sync.Once
	ticked := make(chan struct{})
	_, stop := runLoop(t, Loop{
		Interval: tickInterval,
		Passes: []Pass{{
			DoneMsg: "drained",
			Run: func(context.Context, time.Time) (int, error) {
				i := idx.inc() - 1
				if i >= len(batches) {
					firstTick.Do(func() { close(ticked) })
					return 0, nil
				}
				return batches[i], nil
			},
			Drain: true,
		}},
	}, logger)
	defer stop()

	<-ticked
	// The first tick must have consumed the whole script: 5, 5, 3, then the 0
	// that ends it — four calls, not one.
	if got := idx.get(); got < len(batches) {
		t.Fatalf("drain made %d calls, want at least %d (it stopped before the queue was empty)", got, len(batches))
	}
	waitFor(t, "the drain total to be logged", func() bool { return rec.find("drained") != nil })
	if got := rec.find("drained")["count"]; got != float64(13) {
		t.Errorf("logged count = %v, want 13 (the total across the drain, not the last batch)", got)
	}
}

// TestSinglePassDoesNotRepeat is the other half of the drain axis: a sweep that
// reports work done must NOT be re-run inside the same tick.
func TestSinglePassDoesNotRepeat(t *testing.T) {
	_, logger := newRecorder()
	var calls counter
	_, stop := runLoop(t, Loop{
		Interval: 50 * time.Millisecond,
		Passes: []Pass{{
			Run: func(context.Context, time.Time) (int, error) { calls.inc(); return 7, nil },
		}},
	}, logger)
	defer stop()
	waitFor(t, "the first tick", func() bool { return calls.get() >= 1 })
	if got := calls.get(); got > 1 {
		t.Errorf("a non-draining pass ran %d times in one tick window, want 1", got)
	}
}

// TestErrorIsLoggedAndTheLoopContinues is the availability property: a database
// blip must cost one warning, not the worker.
func TestErrorIsLoggedAndTheLoopContinues(t *testing.T) {
	rec, logger := newRecorder()
	var calls counter
	_, stop := runLoop(t, Loop{
		Interval: tickInterval,
		Passes: []Pass{{
			FailMsg: "widget sweep failed",
			Run: func(context.Context, time.Time) (int, error) {
				if calls.inc() <= 2 {
					return 0, errors.New("boom")
				}
				return 0, nil
			},
		}},
	}, logger)
	defer stop()

	waitFor(t, "the loop to keep ticking past its failures", func() bool { return calls.get() >= 5 })
	got := rec.find("widget sweep failed")
	if got == nil {
		t.Fatal("the pass error was not logged")
	}
	if got["level"] != "WARN" {
		t.Errorf("error logged at %v, want WARN", got["level"])
	}
	if got["error"] != "boom" {
		t.Errorf("logged error = %v, want the cause", got["error"])
	}
}

// TestDrainStopsOnErrorButStillLogsProgress pins the transcode worker's exact
// behaviour: a failing claim query ends the inner loop, and the batches that
// DID complete before it are still reported.
//
// The failure REACHES the caller rather than being logged here, because the
// WARN moved up to Loop.Run when the backoff ladder arrived: only the loop
// knows how many consecutive failures this one is, and that count is half the
// line.
func TestDrainStopsOnErrorButStillLogsProgress(t *testing.T) {
	rec, logger := newRecorder()
	var calls counter
	// One pass is the whole behaviour under test, so it is driven directly
	// rather than waited for through a ticker.
	err := Pass{
		FailMsg: "widget drain failed",
		DoneMsg: "widget drain completed",
		Drain:   true,
		Run: func(context.Context, time.Time) (int, error) {
			if calls.inc() == 1 {
				return 4, nil
			}
			return 0, errors.New("claim query failed")
		},
	}.run(t.Context(), logger, time.Now())

	if got := calls.get(); got != 2 {
		t.Fatalf("drain made %d calls, want 2 (one batch, then the failing claim)", got)
	}
	if err == nil {
		t.Error("the drain error was not returned to the loop")
	}
	done := rec.find("widget drain completed")
	if done == nil {
		t.Fatal("the work completed before the error was not reported")
	}
	if done["count"] != float64(4) {
		t.Errorf("logged count = %v, want 4", done["count"])
	}
}

// TestPassesRunInOrder covers the workers that pair a drain with a sweep, and
// pins that a failing pass does not skip the ones after it — the account-export
// worker sweeps expired archives even when the drain's claim query fails.
func TestPassesRunInOrder(t *testing.T) {
	rec, logger := newRecorder()
	var mu sync.Mutex
	var order []string
	record := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, name)
	}
	_, stop := runLoop(t, Loop{
		Interval: tickInterval,
		Passes: []Pass{
			{
				FailMsg: "first failed",
				Run: func(context.Context, time.Time) (int, error) {
					record("first")
					return 0, errors.New("boom")
				},
			},
			{
				Run: func(context.Context, time.Time) (int, error) { record("second"); return 0, nil },
			},
		},
	}, logger)
	defer stop()

	waitFor(t, "both passes to run", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) >= 2
	})
	mu.Lock()
	first, second := order[0], order[1]
	mu.Unlock()
	if first != "first" || second != "second" {
		t.Errorf("passes ran as %q, %q; want them in declaration order", first, second)
	}
	if rec.find("first failed") == nil {
		t.Error("the first pass's error was not logged")
	}
}

// TestDoneMsgOnlyWhenThereWasWork keeps an idle sweep from writing a line every
// tick forever — several of these run on a one-minute period.
func TestDoneMsgOnlyWhenThereWasWork(t *testing.T) {
	rec, logger := newRecorder()
	var calls counter
	_, stop := runLoop(t, Loop{
		Interval: tickInterval,
		Passes: []Pass{{
			DoneMsg: "swept things",
			Run:     func(context.Context, time.Time) (int, error) { calls.inc(); return 0, nil },
		}},
	}, logger)
	defer stop()
	waitFor(t, "several idle ticks", func() bool { return calls.get() >= 5 })
	if rec.find("swept things") != nil {
		t.Error("an idle pass logged its done message")
	}
}

// TestDoneLevelIsConfigurable covers the transcode hold sweep, a backstop whose
// success IS the alarming case: it only releases videos a crashed worker left
// stuck, so a released batch is logged at warn.
func TestDoneLevelIsConfigurable(t *testing.T) {
	rec, logger := newRecorder()
	_, stop := runLoop(t, Loop{
		Interval: tickInterval,
		Passes: []Pass{{
			DoneMsg:   "backstop released stuck items",
			DoneLevel: slog.LevelWarn,
			Run:       func(context.Context, time.Time) (int, error) { return 2, nil },
		}},
	}, logger)
	defer stop()
	waitFor(t, "the done message", func() bool { return rec.find("backstop released stuck items") != nil })
	if got := rec.find("backstop released stuck items")["level"]; got != "WARN" {
		t.Errorf("done message logged at %v, want WARN", got)
	}
}

// TestTickTimeIsPassedThrough covers the retention and rollup workers, which
// prune "as of" the tick rather than as of whenever the pass happened to start.
func TestTickTimeIsPassedThrough(t *testing.T) {
	_, logger := newRecorder()
	var mu sync.Mutex
	var seen time.Time
	before := time.Now()
	_, stop := runLoop(t, Loop{
		Interval: tickInterval,
		Passes: []Pass{{
			Run: func(_ context.Context, tick time.Time) (int, error) {
				mu.Lock()
				defer mu.Unlock()
				seen = tick
				return 0, nil
			},
		}},
	}, logger)
	defer stop()
	waitFor(t, "a tick", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return !seen.IsZero()
	})
	mu.Lock()
	got := seen
	mu.Unlock()
	if got.Before(before) {
		t.Errorf("tick time %v predates the loop's start %v", got, before)
	}
}

func TestJitterStartReturnsFalseOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if JitterStart(ctx, time.Hour) {
		t.Error("JitterStart returned true for a canceled context")
	}
}

// TestJitterStartWithNonPositiveIntervalIsANoOp pins that a zero interval does
// not panic in rand.Int64N and does not block.
func TestJitterStartWithNonPositiveIntervalIsANoOp(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		if !JitterStart(t.Context(), d) {
			t.Errorf("JitterStart(ctx, %v) = false, want true (nothing to wait for)", d)
		}
	}
}

// TestJitterStartWaitsWithinTheInterval pins the bound: the hold is somewhere
// inside one interval, never longer.
func TestJitterStartWaitsWithinTheInterval(t *testing.T) {
	const interval = 20 * time.Millisecond
	start := time.Now()
	if !JitterStart(t.Context(), interval) {
		t.Fatal("JitterStart returned false on a live context")
	}
	// Generous upper bound: the assertion is "bounded by the interval", not a
	// timing measurement, so scheduling slack must not make it flaky.
	if elapsed := time.Since(start); elapsed > 20*interval {
		t.Errorf("JitterStart held for %v, want well under %v", elapsed, 20*interval)
	}
}

// TestBackoffLadderThinsRepeatedFailures is A34's finding, mechanised. With
// PostgreSQL down every loop retried at its fixed tick for the whole outage —
// measured at exactly 10.000 s apart — so N workers × M loops produced a
// constant flood of identical WARNs for as long as the dependency was gone.
//
// What is asserted is the SHAPE and not a wall-clock schedule: over a window
// that would have allowed dozens of fixed-interval attempts, a permanently
// failing pass must make far fewer, and each WARN must name where on the ladder
// it is so an operator can tell a blip from an outage.
func TestBackoffLadderThinsRepeatedFailures(t *testing.T) {
	rec, logger := newRecorder()
	var calls counter
	_, stop := runLoop(t, Loop{
		Interval: tickInterval,
		Passes: []Pass{{
			FailMsg: "widget sweep failed",
			Run: func(context.Context, time.Time) (int, error) {
				calls.inc()
				return 0, errors.New("postgres is down")
			},
		}},
	}, logger)
	defer stop()

	// Long enough for ~40 un-backed-off ticks; the ladder should allow ~6.
	waitFor(t, "the failing pass to climb its ladder", func() bool { return calls.get() >= 4 })
	time.Sleep(40 * tickInterval)
	if got := calls.get(); got > 12 {
		t.Errorf("failing pass ran %d times in a window that fits ~40 fixed-interval ticks; the ladder is not thinning anything", got)
	}

	got := rec.find("widget sweep failed")
	if got == nil {
		t.Fatal("the pass error was not logged")
	}
	if got["consecutive_failures"] == nil {
		t.Error("the WARN does not say how far up the ladder this failure is")
	}
	if got["retry_in"] == nil {
		t.Error("the WARN does not say when the pass will be tried again")
	}
}

// TestFirstFailureCostsNoDelay pins the low end of the ladder. Backoff(1, …) is
// the interval itself, so one bad tick must not push the next attempt out — a
// single blip and a real outage are different things, and only the second one
// should slow down.
func TestFirstFailureCostsNoDelay(t *testing.T) {
	_, logger := newRecorder()
	var calls counter
	_, stop := runLoop(t, Loop{
		Interval: tickInterval,
		Passes: []Pass{{
			FailMsg: "widget sweep failed",
			Run: func(context.Context, time.Time) (int, error) {
				if calls.inc() == 1 {
					return 0, errors.New("one blip")
				}
				return 1, nil
			},
		}},
	}, logger)
	defer stop()

	waitFor(t, "the tick after a single failure to run on schedule", func() bool { return calls.get() >= 4 })
}

// TestRecoveryIsNeverSilent: a ladder that climbed and then recovered says so.
// Without the line the last thing in the log is a failure, and an operator
// cannot tell a fixed dependency from a forgotten one.
func TestRecoveryIsNeverSilent(t *testing.T) {
	rec, logger := newRecorder()
	var calls counter
	_, stop := runLoop(t, Loop{
		Interval: tickInterval,
		Passes: []Pass{{
			FailMsg: "widget sweep failed",
			Run: func(context.Context, time.Time) (int, error) {
				if calls.inc() <= 2 {
					return 0, errors.New("still down")
				}
				return 1, nil
			},
		}},
	}, logger)
	defer stop()

	waitFor(t, "the recovery line", func() bool { return rec.find("job loop pass recovered") != nil })
	got := rec.find("job loop pass recovered")
	if got["after_failures"] == nil {
		t.Error("the recovery line does not say how many failures preceded it")
	}
}

// TestOneFailingPassDoesNotSilenceItsSibling: the ladder is PER PASS. A worker
// that pairs a drain with a sweep must not lose the drain because the sweep is
// failing — they are different queries against different tables and only one of
// them is broken.
func TestOneFailingPassDoesNotSilenceItsSibling(t *testing.T) {
	_, logger := newRecorder()
	var bad, good counter
	_, stop := runLoop(t, Loop{
		Interval: tickInterval,
		Passes: []Pass{
			{
				FailMsg: "bad pass failed",
				Run: func(context.Context, time.Time) (int, error) {
					bad.inc()
					return 0, errors.New("down")
				},
			},
			{
				FailMsg: "good pass failed",
				Run: func(context.Context, time.Time) (int, error) {
					good.inc()
					return 1, nil
				},
			},
		},
	}, logger)
	defer stop()

	waitFor(t, "the healthy pass to keep its own cadence", func() bool { return good.get() >= 12 })
	if b := bad.get(); b >= good.get() {
		t.Errorf("the failing pass ran %d times and the healthy one %d; the ladder is not per-pass", b, good.get())
	}
}
