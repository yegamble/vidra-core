// Package lease keeps a claimed job's lease alive while the worker runs it.
//
// The durable queues claim work by pushing the row's due time forward by a fixed
// lease. That lease is what makes crash recovery work without anyone having to
// know which instances are alive: a worker that dies stops renewing, the lease
// elapses, and the sweep returns the row to the queue.
//
// The same mechanism is a hazard for a job that legitimately runs longer than
// one lease — a two-hour transcode under a thirty-minute lease would be swept
// back into the queue and started a second time, with both workers writing the
// same output prefix. Renewing on a ticker is what separates "this worker is
// gone" from "this worker is still going".
package lease

import (
	"context"
	"log/slog"
	"time"
)

// DefaultInterval is how often a lease is renewed. The queues lease for 30
// minutes, so renewing every 5 leaves five consecutive renewals' worth of slack
// before a live job is at risk — enough that a brief database stall or a
// saturated worker does not cost a job its lease.
const DefaultInterval = 5 * time.Minute

// Keep renews a lease every interval until the returned stop is called. It
// returns immediately; renewal runs on its own goroutine.
//
// stop is idempotent and safe to defer. renew failures are logged and retried on
// the next tick rather than aborting: a single failed renewal is survivable
// (there is slack before the lease expires), and killing a running job because
// one UPDATE failed would be worse than the risk it is guarding.
//
// ctx bounds the renewals — when it is cancelled, renewal stops. That is correct
// on shutdown: the worker is going away, and letting the lease lapse is exactly
// how another instance picks the job up.
func Keep(ctx context.Context, interval time.Duration, name string, renew func(context.Context) error) (stop func()) {
	if interval <= 0 {
		interval = DefaultInterval
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				// Renewal must not inherit the job's cancellation-in-progress, but
				// it must not outlive the process either; a short bound is enough
				// for one UPDATE by primary key.
				rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				err := renew(rctx)
				cancel()
				if err != nil {
					slog.WarnContext(ctx, "lease renewal failed; will retry on the next tick",
						"lease", name, "error", err.Error())
				}
			}
		}
	}()
	var once bool
	return func() {
		if once {
			return
		}
		once = true
		close(done)
		<-stopped
	}
}
