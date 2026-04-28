package inner_circle

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"vidra-core/internal/metrics"
)

// ExpiryJob runs the membership-expiry sweep on a fixed cadence. It is owned
// by the Inner Circle package — not by the livestream scheduler — so a fault
// in one domain cannot stop the other.
type ExpiryJob struct {
	svc            *MembershipService
	interval       time.Duration
	pendingTimeout time.Duration
	expiredActive  atomic.Int64
	expiredPending atomic.Int64
}

// NewExpiryJob builds a job. interval is how often the sweep runs (defaults to
// 5 minutes if zero); pendingTimeout is how long a pending row may sit before
// being expired (defaults to 1 hour if zero).
func NewExpiryJob(svc *MembershipService, interval, pendingTimeout time.Duration) *ExpiryJob {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if pendingTimeout <= 0 {
		pendingTimeout = time.Hour
	}
	return &ExpiryJob{svc: svc, interval: interval, pendingTimeout: pendingTimeout}
}

// Run blocks until ctx is cancelled, sweeping at every interval tick. Sweep
// failures are logged and the loop continues — a transient DB blip must not
// abort the long-running scheduler.
func (j *ExpiryJob) Run(ctx context.Context) {
	if j == nil || j.svc == nil {
		return
	}
	// Initial sweep so memberships with already-elapsed expires_at are reaped
	// soon after server start, not at the first interval tick.
	j.sweep(ctx)

	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			j.sweep(ctx)
		}
	}
}

// Sweep runs one expiry pass synchronously. Exposed for tests + admin trigger.
func (j *ExpiryJob) Sweep(ctx context.Context) (activeExpired, pendingExpired int, err error) {
	return j.sweep(ctx)
}

func (j *ExpiryJob) sweep(ctx context.Context) (int, int, error) {
	a, p, err := j.svc.RunExpiry(ctx, j.pendingTimeout)
	if err != nil {
		slog.Warn("inner_circle_expiry_failed", "err", err)
		return 0, 0, err
	}
	j.expiredActive.Add(int64(a))
	j.expiredPending.Add(int64(p))
	// Phase 9 — surface to Prometheus so operators can dashboard / alert on
	// expiry sweep activity.
	metrics.IncInnerCircleExpiredActive(a)
	metrics.IncInnerCircleExpiredPending(p)
	if a > 0 || p > 0 {
		slog.Info("inner_circle_expiry_run", "expired_active", a, "expired_pending", p)
	}
	return a, p, nil
}

// Stats returns lifetime totals for observability surfaces.
func (j *ExpiryJob) Stats() (activeExpired, pendingExpired int64) {
	return j.expiredActive.Load(), j.expiredPending.Load()
}
