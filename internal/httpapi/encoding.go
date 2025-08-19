package httpapi

import (
	"net/http"

	"athena/internal/config"
	"athena/internal/scheduler"
	"athena/internal/usecase"
)

// EncodingStatusHandler reports counts of jobs by status
func EncodingStatusHandler(repo usecase.EncodingRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		counts, err := repo.GetJobCounts(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"pending":    counts["pending"],
			"processing": counts["processing"],
			"completed":  counts["completed"],
			"failed":     counts["failed"],
		})
	}
}

// EncodingStatusHandlerEnhanced reports job counts and scheduler status if available.
func EncodingStatusHandlerEnhanced(repo usecase.EncodingRepository, cfg *config.Config, sched *scheduler.EncodingScheduler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		counts, err := repo.GetJobCounts(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err)
			return
		}
		resp := map[string]interface{}{
			"pending":    counts["pending"],
			"processing": counts["processing"],
			"completed":  counts["completed"],
			"failed":     counts["failed"],
		}
		// Attach scheduler status
		resp["scheduler_enabled"] = cfg.EnableEncodingScheduler
		resp["scheduler_interval_seconds"] = cfg.EncodingSchedulerIntervalSeconds
		resp["scheduler_burst"] = cfg.EncodingSchedulerBurst
		if sched != nil {
			st := sched.Snapshot()
			resp["scheduler_last_tick"] = st.LastTick
			resp["scheduler_last_processed"] = st.LastProcessed
		}
		WriteJSON(w, http.StatusOK, resp)
	}
}
