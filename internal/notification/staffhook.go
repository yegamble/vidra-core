package notification

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// StaffQueueHook adapts a staff fan-out into the best-effort callback the auth,
// video and federation seams accept. It is the isolation boundary: an error is
// logged, and a PANIC in the fan-out is recovered and logged too, so a
// notification fault can never fail or crash the registration, quarantine or
// follow that triggered it (AGENTS.md: notifications are best-effort side
// effects). A nil fan (service not built yet) is a no-op.
func StaffQueueHook(logger *slog.Logger, what string, fan func(context.Context, uuid.UUID) (int64, error)) func(context.Context, uuid.UUID) {
	return func(ctx context.Context, id uuid.UUID) {
		if fan == nil {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				logger.Error("staff queue notification panicked", "queue", what, "queue_item_id", id, "panic", r)
			}
		}()
		if _, err := fan(ctx, id); err != nil {
			logger.Warn("staff queue notification failed", "queue", what, "queue_item_id", id, "error", err)
		}
	}
}
