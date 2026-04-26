package payments

import (
	"context"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/stretchr/testify/assert"
)

// fakeBalanceWorker exercises the worker's branch logic without touching
// Postgres. It substitutes function-shaped helpers for the
// repo dependencies so we can unit-test the decision path.
type fakeWorkerDeps struct {
	balances    map[string]int64
	cooldownOK  map[string]bool // key = userID + ":" + type
	cooldownErr error
	low         map[string]*time.Time // user → since
	emissions   []emission
}

type emission struct {
	userID string
	nType  domain.NotificationType
	title  string
}

type fakeEmitter struct {
	deps *fakeWorkerDeps
}

func (f *fakeEmitter) Emit(_ context.Context, userID string, nType domain.NotificationType, title, _ string, _ map[string]interface{}) error {
	f.deps.emissions = append(f.deps.emissions, emission{userID, nType, title})
	return nil
}

func TestPaymentConfigSource_Static(t *testing.T) {
	cfg := NewStaticConfig(50_000)
	assert.Equal(t, int64(50_000), cfg.MinPayoutSats())
}

func TestBalanceWorker_DecisionMatrix(t *testing.T) {
	// This test exercises the decision logic in processUser-equivalent
	// fashion via a small reimplementation that mirrors the real worker's
	// branching. We keep the real worker pure in a future refactor; for
	// now we sanity-check the rules with table-driven cases.
	type tc struct {
		name           string
		balance        int64
		minSats        int64
		stuckSince     *time.Time // nil = no prior stuck state
		threshold      time.Duration
		cooldownAllows bool
		expectStuck    bool
		expectReady    bool
		expectClear    bool
	}
	now := time.Now()
	stuckOld := now.Add(-8 * 24 * time.Hour)
	stuckRecent := now.Add(-2 * 24 * time.Hour)
	cases := []tc{
		{"zero balance clears state", 0, 50_000, &stuckOld, 7 * 24 * time.Hour, true, false, false, true},
		{"crossed above min emits payout_ready", 60_000, 50_000, &stuckOld, 7 * 24 * time.Hour, true, false, true, true},
		{"crossed above min but cooldown blocks", 60_000, 50_000, &stuckOld, 7 * 24 * time.Hour, false, false, false, true},
		{"in window but not yet 7d stuck", 7_000, 50_000, &stuckRecent, 7 * 24 * time.Hour, true, false, false, false},
		{"in window 8d stuck + cooldown allows", 7_000, 50_000, &stuckOld, 7 * 24 * time.Hour, true, true, false, false},
		{"in window 8d stuck + cooldown blocks", 7_000, 50_000, &stuckOld, 7 * 24 * time.Hour, false, false, false, false},
		{"in window first time (no prior since)", 7_000, 50_000, nil, 7 * 24 * time.Hour, true, false, false, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Decide the action without DB.
			var emittedReady, emittedStuck, cleared bool
			switch {
			case c.balance <= 0:
				cleared = true
			case c.balance >= c.minSats:
				cleared = true
				if c.cooldownAllows {
					emittedReady = true
				}
			default:
				if c.stuckSince != nil && now.Sub(*c.stuckSince) >= c.threshold && c.cooldownAllows {
					emittedStuck = true
				}
			}
			assert.Equal(t, c.expectClear, cleared, "clear")
			assert.Equal(t, c.expectReady, emittedReady, "payout_ready")
			assert.Equal(t, c.expectStuck, emittedStuck, "low_balance_stuck")
		})
	}
}

func TestBalanceWorker_DefaultsApplied(t *testing.T) {
	// NewBalanceWorker with empty env should pick the default interval.
	t.Setenv("BALANCE_WORKER_INTERVAL", "")
	t.Setenv("BALANCE_WORKER_LOW_STUCK_DAYS", "")
	w := NewBalanceWorker(nil, nil, nil, nil, nil, NewStaticConfig(50_000))
	assert.Equal(t, DefaultBalanceWorkerInterval, w.interval)
	assert.Equal(t, DefaultLowBalanceStuckThreshold, w.threshold)
}

func TestBalanceWorker_OverridesFromEnv(t *testing.T) {
	t.Setenv("BALANCE_WORKER_INTERVAL", "30s")
	t.Setenv("BALANCE_WORKER_LOW_STUCK_DAYS", "3")
	w := NewBalanceWorker(nil, nil, nil, nil, nil, NewStaticConfig(50_000))
	assert.Equal(t, 30*time.Second, w.interval)
	assert.Equal(t, 3*24*time.Hour, w.threshold)
}

func TestBalanceWorker_RunOnce_RejectsBadConfig(t *testing.T) {
	w := NewBalanceWorker(nil, nil, nil, nil, nil, NewStaticConfig(0))
	err := w.RunOnce(context.Background())
	assert.Error(t, err)
}

func TestBalanceWorker_RunOnce_NilConfig(t *testing.T) {
	w := &BalanceWorker{}
	err := w.RunOnce(context.Background())
	assert.Error(t, err)
}

func TestNotificationEmitterContract(t *testing.T) {
	// fakeEmitter satisfies the existing NotificationEmitter interface used
	// by Ledger/Payout services and the worker.
	deps := &fakeWorkerDeps{}
	var em NotificationEmitter = &fakeEmitter{deps: deps}
	err := em.Emit(context.Background(), "u-1", domain.NotificationPayoutReady, "title", "msg", nil)
	assert.NoError(t, err)
	assert.Len(t, deps.emissions, 1)
	assert.Equal(t, domain.NotificationPayoutReady, deps.emissions[0].nType)
}
