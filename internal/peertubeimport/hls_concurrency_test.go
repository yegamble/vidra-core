package peertubeimport

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

type hlsWorkerResult struct {
	counts Counts
	err    error
}

func hlsWorkerTargets(n int) []hlsCopyTarget {
	targets := make([]hlsCopyTarget, n)
	for i := range targets {
		targets[i].sourceID = strconv.Itoa(i)
	}
	return targets
}

func waitHLSWorkers(t *testing.T, entered <-chan struct{}, cancel context.CancelFunc, done <-chan hlsWorkerResult) {
	t.Helper()
	for range 4 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			cancel()
			<-done
			t.Fatal("four HLS trees did not make progress concurrently")
		}
	}
}

func TestHLSWorkersBoundConcurrencyAndAggregateFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release := make(chan struct{}, 12), make(chan struct{})
	done := make(chan hlsWorkerResult, 1)
	var active, maximum atomic.Int32
	var visits [12]atomic.Int32
	go func() {
		counts, err := runHLSCopyWorkers(ctx, hlsWorkerTargets(12), func(ctx context.Context, target hlsCopyTarget) (hlsCopyOutcome, error) {
			n := active.Add(1)
			defer active.Add(-1)
			for max := maximum.Load(); n > max && !maximum.CompareAndSwap(max, n); max = maximum.Load() {
			}
			id, _ := strconv.Atoi(target.sourceID)
			visits[id].Add(1)
			entered <- struct{}{}
			select {
			case <-release:
				return hlsCopyOutcome(id % 3), nil // Failed or superseded trees do not stop other trees.
			case <-ctx.Done():
				return hlsCopyFailed, ctx.Err()
			}
		})
		done <- hlsWorkerResult{counts, err}
	}()
	waitHLSWorkers(t, entered, cancel, done)
	if active.Load() != 4 {
		t.Errorf("active workers=%d, want 4", active.Load())
	}
	close(release)
	result := <-done
	if result.err != nil || result.counts.Imported != 4 || result.counts.Failed != 4 || result.counts.Skipped != 4 || maximum.Load() != 4 || active.Load() != 0 {
		t.Fatalf("result=%+v maximum=%d active=%d", result, maximum.Load(), active.Load())
	}
	for i := range visits {
		if visits[i].Load() != 1 {
			t.Errorf("target %d visited %d times", i, visits[i].Load())
		}
	}
}

func TestHLSWorkersCancelAndJoinOnDatabaseFailure(t *testing.T) {
	for _, databaseFailure := range []bool{false, true} {
		t.Run(strconv.FormatBool(databaseFailure), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, failNow := make(chan struct{}, 8), make(chan struct{})
			done := make(chan hlsWorkerResult, 1)
			var started, joined atomic.Int32
			databaseErr := errors.New("database unavailable")
			go func() {
				counts, err := runHLSCopyWorkers(ctx, hlsWorkerTargets(8), func(ctx context.Context, target hlsCopyTarget) (hlsCopyOutcome, error) {
					started.Add(1)
					defer joined.Add(1)
					entered <- struct{}{}
					if databaseFailure && target.sourceID == "0" {
						select {
						case <-failNow:
							return hlsCopyFailed, databaseErr
						case <-ctx.Done():
							return hlsCopyFailed, ctx.Err()
						}
					}
					<-ctx.Done()
					return hlsCopyFailed, ctx.Err()
				})
				done <- hlsWorkerResult{counts, err}
			}()
			waitHLSWorkers(t, entered, cancel, done)
			want := context.Canceled
			if databaseFailure {
				close(failNow)
				want = databaseErr
			} else {
				cancel()
			}
			select {
			case result := <-done:
				if !errors.Is(result.err, want) || result.counts.Imported != 0 || result.counts.Failed != 0 || result.counts.Skipped != 0 || started.Load() != 4 || joined.Load() != 4 {
					t.Fatalf("result=%+v started=%d joined=%d", result, started.Load(), joined.Load())
				}
			case <-time.After(2 * time.Second):
				t.Fatal("HLS workers did not join after cancellation")
			}
		})
	}
}

func TestHLSWorkersConfiguredBound(t *testing.T) {
	for _, limit := range []int{1, 8, 16, 32, 64} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan struct{}, 64)
			done := make(chan error, 1)
			go func() {
				_, err := runHLSCopyWorkersWithProgress(ctx, hlsWorkerTargets(64), func(ctx context.Context, _ hlsCopyTarget) (hlsCopyOutcome, error) {
					entered <- struct{}{}
					<-ctx.Done()
					return hlsCopyFailed, ctx.Err()
				}, nil, limit)
				done <- err
			}()
			for range min(limit, 32) {
				select {
				case <-entered:
				case <-time.After(2 * time.Second):
					cancel()
					<-done
					t.Fatal("configured workers did not start")
				}
			}
			select {
			case <-entered:
				t.Error("worker bound exceeded")
			default:
			}
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}
