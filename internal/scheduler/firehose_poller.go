package scheduler

import (
	"context"
	"sync"
	"time"

	"vidra-core/internal/metrics"
	"vidra-core/internal/usecase"
)

// FirehosePoller is a lightweight, near real-time ingestor that repeatedly
// triggers one federation processing tick at a short interval. It reuses the
// same ingestion path as the standard FederationScheduler (ProcessNext), but
// at a tighter cadence to approximate a firehose.
type FirehosePoller struct {
	svc      usecase.FederationService
	interval time.Duration
	burst    int
	mu       sync.Mutex
	cancel   context.CancelFunc
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
	localCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.cancel = cancel
	p.mu.Unlock()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-localCtx.Done():
			return
		case <-ticker.C:
			processed := 0
			for i := 0; i < p.burst; i++ {
				ok, _ := p.svc.ProcessNext(localCtx)
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

func (p *FirehosePoller) Stop() {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
	}
	p.mu.Unlock()
}
