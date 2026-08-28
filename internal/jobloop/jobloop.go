// Package jobloop is the ticker skeleton every background worker in the api
// process runs on.
//
// cmd/api had twenty-odd of these written out by hand, and they agreed on
// everything that matters: jitter the start so a fleet does not phase-lock,
// create a ticker, exit on context cancellation, skip the tick when this
// instance is not the leader, do the work, log a warning on failure and a count
// on success. What they varied was which of those parts they had — and, because
// they were copies, one of them would eventually vary by accident instead.
//
// Nothing here is clever. The point is that the four decisions a worker makes —
// jitter or not, leader-gated or not, drain to empty or one pass, one operation
// per tick or several — are now written down as a value you can read at the top
// of the worker, instead of inferred from twenty lines of nesting.
//
// Not every worker fits, and the ones that do not stay hand-rolled: a loop with
// two tickers of different periods, or with a boot-time timer racing its
// ticker, is a different shape, and bending it to fit here would cost more than
// the copy does.
package jobloop

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"
)

// Leader gates a loop to a single instance. It is satisfied by
// *leaderlock.Elector, including a nil one — which reports itself leader,
// because "election was never wired" means single-instance, where everything
// must run. A nil Leader FIELD is different: it means the loop is not gated at
// all.
type Leader interface{ IsLeader() bool }

// Pass is one operation a tick performs. Most loops have exactly one; a few
// pair a drain with a sweep.
type Pass struct {
	// Run does the work and reports how many items it handled. tick is the time
	// the ticker fired, for the passes that prune or roll up "as of now".
	Run func(ctx context.Context, tick time.Time) (int, error)
	// FailMsg is logged at warn with the error. Per-item failures belong in the
	// queue (retry/backoff/dead-letter); what reaches here is the claim or sweep
	// query itself failing.
	FailMsg string
	// DoneMsg, when set, is logged with "count" whenever the pass handled at
	// least one item. Empty means the pass logs nothing on success — either it
	// has nothing worth counting, or its Run does its own richer logging.
	DoneMsg string
	// DoneLevel is the level for DoneMsg. The zero value is slog.LevelInfo; a
	// backstop that should never have work to do sets warn.
	DoneLevel slog.Level
	// Drain repeats Run until it reports zero (or fails), so a burst does not
	// wait a whole tick per batch. The count logged is the total across the
	// repeats. Run MUST report only completed work for this to terminate — a
	// pass that counts attempts would spin on a permanently failing item.
	Drain bool
}

// Loop is a worker's whole schedule.
type Loop struct {
	// Interval is the tick period. It must be positive; a worker with a
	// configurable interval defaults it before building the Loop.
	Interval time.Duration
	// Jitter holds the start for a random slice of Interval so a fleet's
	// tickers land on different phases. On for anything every instance runs,
	// off for leader-gated loops (exactly one instance acts, so there is no
	// phase to spread).
	Jitter bool
	// Leader, when set, gates each tick: a follower skips it rather than
	// shutting down, because leadership can move at any time.
	Leader Leader
	// Passes run in order, every tick. A pass that fails does not skip the ones
	// after it.
	Passes []Pass
}

// Run drives the loop until ctx is canceled. It blocks; call it in a goroutine.
func (l Loop) Run(ctx context.Context, logger *slog.Logger) {
	if l.Jitter && !JitterStart(ctx, l.Interval) {
		return
	}
	ticker := time.NewTicker(l.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case tick := <-ticker.C:
			// Singleton sweep: exactly one instance runs it. A follower skips the
			// tick rather than shutting down, because leadership can move here at
			// any time (see internal/leaderlock).
			if l.Leader != nil && !l.Leader.IsLeader() {
				continue
			}
			for _, p := range l.Passes {
				p.run(ctx, logger, tick)
			}
		}
	}
}

func (p Pass) run(ctx context.Context, logger *slog.Logger, tick time.Time) {
	total := 0
	for {
		n, err := p.Run(ctx, tick)
		if err != nil {
			logger.Warn(p.FailMsg, "error", err)
			break
		}
		total += n
		if !p.Drain || n == 0 {
			break
		}
	}
	if total > 0 && p.DoneMsg != "" {
		logger.Log(ctx, p.DoneLevel, p.DoneMsg, "count", total)
	}
}

// JitterStart holds the caller for a uniformly random slice of interval, so the
// ticker it is about to create lands on a random phase. It returns false if ctx
// ended while waiting, which is the caller's signal to return instead of
// starting its loop.
//
// Every worker in a deployment boots within seconds of its siblings — that is
// what a rolling deploy IS — and a ticker created at boot fires on its creation
// phase for the entire life of the process. Un-jittered, a fleet of N workers
// therefore hits the database with N simultaneous claim queries every interval
// and then sits idle for the rest of it, forever: the load is N× peaky for the
// same throughput, and the peak is exactly when every instance is also
// contending for the same queue rows. Randomising the phase once, at start,
// spreads the same work into a trickle and costs one bounded sleep.
//
// Deliberately not configurable: a knob here would be a knob whose only correct
// setting is "on", and an operator who set it wrong would get the phase-locked
// behaviour back with no symptom to trace it by.
//
// Exported because the loops that do not fit Loop still need it.
func JitterStart(ctx context.Context, interval time.Duration) bool {
	if interval <= 0 {
		return true
	}
	t := time.NewTimer(time.Duration(rand.Int64N(int64(interval))))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
