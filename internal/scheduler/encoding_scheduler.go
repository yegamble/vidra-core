package scheduler

import (
    "context"
    "time"

    "athena/internal/usecase"
)

// EncodingScheduler periodically drains the encoding queue by invoking
// EncodingService.ProcessNext at a fixed interval. It loops within a tick
// to process multiple jobs up to a burst limit.
type EncodingScheduler struct {
    svc     usecase.EncodingService
    interval time.Duration
    burst    int
}

// NewEncodingScheduler creates a new scheduler.
// interval: how often to poll the queue
// burst: maximum number of jobs to process per tick (>=1)
func NewEncodingScheduler(svc usecase.EncodingService, interval time.Duration, burst int) *EncodingScheduler {
    if burst <= 0 {
        burst = 1
    }
    if interval <= 0 {
        interval = 5 * time.Second
    }
    return &EncodingScheduler{svc: svc, interval: interval, burst: burst}
}

// Start runs the scheduler until ctx is canceled.
func (s *EncodingScheduler) Start(ctx context.Context) {
    ticker := time.NewTicker(s.interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            // Drain up to burst jobs per tick
            for i := 0; i < s.burst; i++ {
                processed, _ := s.svc.ProcessNext(ctx)
                if !processed {
                    break
                }
            }
        }
    }
}

