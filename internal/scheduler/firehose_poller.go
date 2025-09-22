package scheduler

import (
	"context"
	"time"

	"athena/internal/metrics"
	"athena/internal/usecase"
)

// FirehosePoller is a lightweight, near real-time ingestor that repeatedly
// triggers one federation processing tick at a short interval. It reuses the
// same ingestion path as the standard FederationScheduler (ProcessNext), but
// at a tighter cadence to approximate a firehose.
type FirehosePoller struct {
	svc      usecase.FederationService
	interval time.Duration
	burst    int
}

func NewFirehosePoller(svc usecase.FederationService, interval time.Duration, burst int) *FirehosePoller {
	if burst <= 0 {
		burst = 3
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &FirehosePoller{svc: svc, interval: interval, burst: burst}
}

func (p *FirehosePoller) Start(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			processed := 0
			for i := 0; i < p.burst; i++ {
				ok, _ := p.svc.ProcessNext(ctx)
				if !ok {
					break
				}
				processed++
			}
			// record a generic tick metric
			metrics.SetSchedulerTick(time.Now())
			_ = processed
		}
	}
}
