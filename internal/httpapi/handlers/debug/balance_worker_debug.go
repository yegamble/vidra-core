//go:build debug

package debug

import (
	"context"
	"log/slog"
	"net/http"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
)

// WorkerTicker is the minimal interface we need from the BalanceWorker
// to fire a manual tick. Defined here to avoid importing the usecase
// package directly (which would couple the debug package to balance
// worker internals).
type WorkerTicker interface {
	RunOnce(ctx context.Context) error
}

// workerTickHandler is the live HTTP handler reference; main.go calls
// SetWorkerTickHandler at startup with a closure over the BalanceWorker.
var workerTickHandler http.HandlerFunc

// SetWorkerTickHandler is called by main.go in debug builds to wire the
// BalanceWorker into the debug endpoint. Subsequent calls overwrite.
// In non-debug builds this function is undefined → callers must guard
// with `debug.Enabled` (or use build tags around their call site).
func SetWorkerTickHandler(worker WorkerTicker) {
	workerTickHandler = func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		slog.Warn(
			"debug: balance worker tick invoked",
			"admin_user_id", userID,
			"path", r.URL.Path,
		)
		if err := worker.RunOnce(r.Context()); err != nil {
			shared.WriteError(w, http.StatusInternalServerError,
				domain.NewDomainError("WORKER_TICK_FAILED", err.Error()))
			return
		}
		shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ticked"})
	}
}
