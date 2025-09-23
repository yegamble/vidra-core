package scheduler

import (
	"context"
	"sync"
	"time"

	"athena/internal/metrics"
	"athena/internal/usecase"
)

// FederationScheduler periodically processes federation jobs and ingestion.
type FederationScheduler struct {
	svc      usecase.FederationService
	interval time.Duration
	burst    int

	mu     sync.RWMutex
	status Status
	cancel context.CancelFunc
}

func NewFederationScheduler(svc usecase.FederationService, interval time.Duration, burst int) *FederationScheduler {
	if burst <= 0 {
		burst = 1
	}
	if interval <= 0 {
		interval = 10 * time.Second
	}
	s := &FederationScheduler{svc: svc, interval: interval, burst: burst}
	s.status = Status{IntervalSeconds: int(interval / time.Second), Burst: burst}
	metrics.SetSchedulerConfig(true, s.status.IntervalSeconds, s.status.Burst)
	return s
}

func (s *FederationScheduler) Snapshot() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *FederationScheduler) Start(ctx context.Context) {
	localCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-localCtx.Done():
			return
		case <-ticker.C:
			processed := 0
			for i := 0; i < s.burst; i++ {
				ok, _ := s.svc.ProcessNext(localCtx)
				if !ok {
					break
				}
				processed++
			}
			s.mu.Lock()
			s.status.LastTick = time.Now()
			s.status.LastProcessed = processed
			s.mu.Unlock()
			metrics.SetSchedulerTick(s.status.LastTick)
		}
	}
}

func (s *FederationScheduler) Stop() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
}
