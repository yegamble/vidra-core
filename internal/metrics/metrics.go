package metrics

import (
    "fmt"
    "net/http"
    "sync/atomic"
)

var (
    encoderJobsProcessed int64
    encoderJobsFailed    int64
    encoderJobsInFlight  int64
)

func IncProcessed() { atomic.AddInt64(&encoderJobsProcessed, 1) }
func IncFailed()    { atomic.AddInt64(&encoderJobsFailed, 1) }
func IncInFlight()  { atomic.AddInt64(&encoderJobsInFlight, 1) }
func DecInFlight()  { atomic.AddInt64(&encoderJobsInFlight, -1) }

// Handler exposes metrics in Prometheus text exposition format
func Handler(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "text/plain; version=0.0.4")
    _, _ = fmt.Fprintf(w, "# TYPE athena_encoder_jobs_processed_total counter\n")
    _, _ = fmt.Fprintf(w, "athena_encoder_jobs_processed_total %d\n", atomic.LoadInt64(&encoderJobsProcessed))
    _, _ = fmt.Fprintf(w, "# TYPE athena_encoder_jobs_failed_total counter\n")
    _, _ = fmt.Fprintf(w, "athena_encoder_jobs_failed_total %d\n", atomic.LoadInt64(&encoderJobsFailed))
    _, _ = fmt.Fprintf(w, "# TYPE athena_encoder_jobs_in_progress gauge\n")
    _, _ = fmt.Fprintf(w, "athena_encoder_jobs_in_progress %d\n", atomic.LoadInt64(&encoderJobsInFlight))
}

