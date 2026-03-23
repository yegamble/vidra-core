package metrics

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

var (
	encoderJobsProcessed int64
	encoderJobsFailed    int64
	encoderJobsInFlight  int64

	federationJobsProcessed int64
	federationJobsFailed    int64
	federationPostsIngested int64

	// Scheduler metrics
	schedulerEnabled         int64 // 0/1
	schedulerIntervalSeconds int64
	schedulerBurst           int64
	schedulerLastTickUnix    int64
)

func IncProcessed() { atomic.AddInt64(&encoderJobsProcessed, 1) }
func IncFailed()    { atomic.AddInt64(&encoderJobsFailed, 1) }
func IncInFlight()  { atomic.AddInt64(&encoderJobsInFlight, 1) }
func DecInFlight()  { atomic.AddInt64(&encoderJobsInFlight, -1) }

// Handler exposes metrics in Prometheus text exposition format
func Handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, "# TYPE vidra_encoder_jobs_processed_total counter\n")
	_, _ = fmt.Fprintf(w, "vidra_encoder_jobs_processed_total %d\n", atomic.LoadInt64(&encoderJobsProcessed))
	_, _ = fmt.Fprintf(w, "# TYPE vidra_encoder_jobs_failed_total counter\n")
	_, _ = fmt.Fprintf(w, "vidra_encoder_jobs_failed_total %d\n", atomic.LoadInt64(&encoderJobsFailed))
	_, _ = fmt.Fprintf(w, "# TYPE vidra_encoder_jobs_in_progress gauge\n")
	_, _ = fmt.Fprintf(w, "vidra_encoder_jobs_in_progress %d\n", atomic.LoadInt64(&encoderJobsInFlight))

	// Federation metrics
	_, _ = fmt.Fprintf(w, "# TYPE vidra_federation_jobs_processed_total counter\n")
	_, _ = fmt.Fprintf(w, "vidra_federation_jobs_processed_total %d\n", atomic.LoadInt64(&federationJobsProcessed))
	_, _ = fmt.Fprintf(w, "# TYPE vidra_federation_jobs_failed_total counter\n")
	_, _ = fmt.Fprintf(w, "vidra_federation_jobs_failed_total %d\n", atomic.LoadInt64(&federationJobsFailed))
	_, _ = fmt.Fprintf(w, "# TYPE vidra_federation_posts_ingested_total counter\n")
	_, _ = fmt.Fprintf(w, "vidra_federation_posts_ingested_total %d\n", atomic.LoadInt64(&federationPostsIngested))

	// Scheduler metrics
	_, _ = fmt.Fprintf(w, "# TYPE vidra_scheduler_enabled gauge\n")
	_, _ = fmt.Fprintf(w, "vidra_scheduler_enabled %d\n", atomic.LoadInt64(&schedulerEnabled))
	_, _ = fmt.Fprintf(w, "# TYPE vidra_scheduler_interval_seconds gauge\n")
	_, _ = fmt.Fprintf(w, "vidra_scheduler_interval_seconds %d\n", atomic.LoadInt64(&schedulerIntervalSeconds))
	_, _ = fmt.Fprintf(w, "# TYPE vidra_scheduler_burst gauge\n")
	_, _ = fmt.Fprintf(w, "vidra_scheduler_burst %d\n", atomic.LoadInt64(&schedulerBurst))
	_, _ = fmt.Fprintf(w, "# TYPE vidra_scheduler_last_tick_unixtime gauge\n")
	_, _ = fmt.Fprintf(w, "vidra_scheduler_last_tick_unixtime %d\n", atomic.LoadInt64(&schedulerLastTickUnix))
}

func IncFedJobsProcessed()      { atomic.AddInt64(&federationJobsProcessed, 1) }
func IncFedJobsFailed()         { atomic.AddInt64(&federationJobsFailed, 1) }
func AddFedPostsIngested(n int) { atomic.AddInt64(&federationPostsIngested, int64(n)) }

// SetSchedulerConfig sets static scheduler metrics (enabled, interval, burst).
func SetSchedulerConfig(enabled bool, intervalSec int, burst int) {
	if enabled {
		atomic.StoreInt64(&schedulerEnabled, 1)
	} else {
		atomic.StoreInt64(&schedulerEnabled, 0)
	}
	atomic.StoreInt64(&schedulerIntervalSeconds, int64(intervalSec))
	atomic.StoreInt64(&schedulerBurst, int64(burst))
}

// SetSchedulerTick updates the last tick timestamp.
func SetSchedulerTick(t time.Time) {
	atomic.StoreInt64(&schedulerLastTickUnix, t.Unix())
}
