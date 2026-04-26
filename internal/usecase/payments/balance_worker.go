package payments

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/repository"

	"github.com/jmoiron/sqlx"
)

// DefaultBalanceWorkerInterval is the production tick cadence (1h). E2E
// debug-tagged builds tick on demand via the /api/v1/debug/balance-worker/tick
// endpoint (Task 13) which calls Worker.RunOnce directly.
const DefaultBalanceWorkerInterval = time.Hour

// DefaultLowBalanceStuckThreshold is how long a user's balance must remain
// in the (0, MIN_PAYOUT_SATS) window before a low_balance_stuck notification
// is emitted. Per spec: 7 days.
const DefaultLowBalanceStuckThreshold = 7 * 24 * time.Hour

// DefaultCooldownHours is the per-user-per-notification-type rate limit on
// emissions. 24h prevents the worker from spamming a user with the same
// reminder on every tick.
const DefaultCooldownHours = 24

// PaymentConfigSource exposes the tunables the worker needs from the
// runtime PaymentConfig (currently just MinPayoutSats).
type PaymentConfigSource interface {
	MinPayoutSats() int64
}

// staticConfig is a trivial PaymentConfigSource returning a fixed value.
// Used by app.go when wiring the worker before the full config service is
// available, and by tests.
type staticConfig int64

func (s staticConfig) MinPayoutSats() int64 { return int64(s) }

// NewStaticConfig constructs a PaymentConfigSource returning a fixed value.
func NewStaticConfig(v int64) PaymentConfigSource { return staticConfig(v) }

// BalanceWorker emits payout_ready and low_balance_stuck notifications on
// a periodic tick. Idempotency is enforced via the
// payment_notification_cooldowns table (24h per user/type) and stuck-state
// is tracked in user_low_balance_state.
type BalanceWorker struct {
	db        *sqlx.DB
	ledger    *repository.PaymentLedgerRepository
	cooldowns *repository.PaymentNotificationCooldownsRepository
	state     *repository.UserLowBalanceStateRepository
	notifier  NotificationEmitter
	cfg       PaymentConfigSource
	interval  time.Duration
	threshold time.Duration
	now       func() time.Time // injectable for tests
}

// NewBalanceWorker constructs a worker. The interval defaults to 1h
// unless BALANCE_WORKER_INTERVAL env (Go duration string) overrides it.
func NewBalanceWorker(
	db *sqlx.DB,
	ledger *repository.PaymentLedgerRepository,
	cooldowns *repository.PaymentNotificationCooldownsRepository,
	state *repository.UserLowBalanceStateRepository,
	notifier NotificationEmitter,
	cfg PaymentConfigSource,
) *BalanceWorker {
	interval := DefaultBalanceWorkerInterval
	if v := os.Getenv("BALANCE_WORKER_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			interval = d
		}
	}
	threshold := DefaultLowBalanceStuckThreshold
	if v := os.Getenv("BALANCE_WORKER_LOW_STUCK_DAYS"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 {
			threshold = time.Duration(d) * 24 * time.Hour
		}
	}
	return &BalanceWorker{
		db:        db,
		ledger:    ledger,
		cooldowns: cooldowns,
		state:     state,
		notifier:  notifier,
		cfg:       cfg,
		interval:  interval,
		threshold: threshold,
		now:       time.Now,
	}
}

// Start runs the worker until ctx is cancelled. Blocks on the ticker.
// SIGTERM-driven shutdown is handled by the caller cancelling ctx.
func (w *BalanceWorker) Start(ctx context.Context) {
	slog.Info("balance worker started", "interval", w.interval, "threshold", w.threshold)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Tick once immediately so freshly-deployed instances don't wait an hour
	// to surface stuck users.
	if err := w.RunOnce(ctx); err != nil {
		slog.Warn("balance worker initial tick failed", "err", err)
	}

	for {
		select {
		case <-ctx.Done():
			slog.Info("balance worker shutting down")
			return
		case <-ticker.C:
			if err := w.RunOnce(ctx); err != nil {
				slog.Warn("balance worker tick failed", "err", err)
			}
		}
	}
}

// RunOnce executes a single tick — visible for the build-tag-gated debug
// endpoint (Task 13) and for unit tests.
func (w *BalanceWorker) RunOnce(ctx context.Context) error {
	if w.cfg == nil {
		return fmt.Errorf("balance worker has no PaymentConfigSource")
	}
	minSats := w.cfg.MinPayoutSats()
	if minSats <= 0 {
		return fmt.Errorf("min_payout_sats must be > 0, got %d", minSats)
	}

	users, err := w.candidateUsers(ctx)
	if err != nil {
		return fmt.Errorf("listing candidate users: %w", err)
	}

	for _, userID := range users {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := w.processUser(ctx, userID, minSats); err != nil {
			slog.Warn("balance worker user processing failed", "user_id", userID, "err", err)
			// Continue — one bad user doesn't poison the whole tick.
		}
	}
	return nil
}

func (w *BalanceWorker) candidateUsers(ctx context.Context) ([]string, error) {
	rows, err := w.db.QueryxContext(ctx, `
		SELECT DISTINCT user_id::text FROM payment_ledger
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		users = append(users, id)
	}
	return users, rows.Err()
}

func (w *BalanceWorker) processUser(ctx context.Context, userID string, minSats int64) error {
	balance, err := w.ledger.GetAvailableBalance(ctx, w.db, userID)
	if err != nil {
		return fmt.Errorf("getting balance: %w", err)
	}

	switch {
	case balance <= 0:
		// Out of the low-balance window (zero or negative — unlikely but
		// treated as a non-stuck state).
		return w.state.ClearLow(ctx, userID)
	case balance >= minSats:
		// Crossed above the threshold; clear stuck state and emit
		// payout_ready (cooldown-gated).
		if err := w.state.ClearLow(ctx, userID); err != nil {
			return fmt.Errorf("clearing low state: %w", err)
		}
		ok, err := w.cooldowns.TryEmit(ctx, userID, string(domain.NotificationPayoutReady), DefaultCooldownHours)
		if err != nil {
			return fmt.Errorf("cooldown try-emit: %w", err)
		}
		if ok && w.notifier != nil {
			if err := w.notifier.Emit(ctx, userID, domain.NotificationPayoutReady,
				"You're ready for a payout",
				fmt.Sprintf("Your balance is %d sats — request a payout from your wallet.", balance),
				map[string]interface{}{"balance_sats": balance, "min_payout_sats": minSats},
			); err != nil {
				slog.Warn("balance worker payout_ready emit failed", "user_id", userID, "err", err)
			}
		}
		return nil
	default:
		// 0 < balance < minSats — in the low-balance window.
		if err := w.state.MarkLow(ctx, userID, balance); err != nil {
			return fmt.Errorf("marking low: %w", err)
		}
		// Only emit when stuck for >= threshold AND cooldown elapsed.
		s, err := w.state.Get(ctx, userID)
		if err != nil {
			return fmt.Errorf("re-reading low state: %w", err)
		}
		if s == nil {
			return nil
		}
		if w.now().Sub(s.Since) < w.threshold {
			return nil
		}
		ok, err := w.cooldowns.TryEmit(ctx, userID, string(domain.NotificationLowBalanceStuck), DefaultCooldownHours)
		if err != nil {
			return fmt.Errorf("cooldown try-emit: %w", err)
		}
		if ok && w.notifier != nil {
			if err := w.notifier.Emit(ctx, userID, domain.NotificationLowBalanceStuck,
				"Your wallet balance is below the payout minimum",
				fmt.Sprintf("Your balance has been at %d sats (below the %d sat minimum) for over a week.", balance, minSats),
				map[string]interface{}{"balance_sats": balance, "min_payout_sats": minSats},
			); err != nil {
				slog.Warn("balance worker low_balance_stuck emit failed", "user_id", userID, "err", err)
			}
		}
		return nil
	}
}
