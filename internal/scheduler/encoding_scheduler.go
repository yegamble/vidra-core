package scheduler

import (
	"context"
	"sync"
	"time"

	"vidra-core/internal/metrics"
	"vidra-core/internal/usecase/encoding"
)

// Status captures recent scheduler activity for observability.
type Status struct {
	IntervalSeconds int       `json:"interval_seconds"`
	Burst           int       `json:"burst"`
	LastTick        time.Time `json:"last_tick"`
	LastProcessed   int       `json:"last_processed"`
}

// EncodingScheduler periodically drains the encoding queue by invoking
// EncodingService.ProcessNext at a fixed interval. It loops within a tick
// to process multiple jobs up to a burst limit.
type EncodingScheduler struct {
	svc      encoding.Service
	interval time.Duration
	burst    int

	mu     sync.RWMutex
	status Status
	cancel context.CancelFunc
	paused bool
}

// NewEncodingScheduler creates a new scheduler.
// interval: how often to poll the queue
// burst: maximum number of jobs to process per tick (>=1)
func NewEncodingScheduler(svc encoding.Service, interval time.Duration, burst int) *EncodingScheduler {
	if burst <= 0 {
		burst = 1
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	s := &EncodingScheduler{svc: svc, interval: interval, burst: burst}
	s.status = Status{IntervalSeconds: int(interval / time.Second), Burst: burst}
	// Seed metrics with config
	metrics.SetSchedulerConfig(true, s.status.IntervalSeconds, s.status.Burst)
	return s
}

// Snapshot returns a copy of the current status.
func (s *EncodingScheduler) Snapshot() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

// Start runs the scheduler until ctx is canceled or Stop is called.
func (s *EncodingScheduler) Start(ctx context.Context) {
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
			s.mu.RLock()
			isPaused := s.paused
			s.mu.RUnlock()
			if isPaused {
				continue
			}
			processedCount := 0
			// Drain up to burst jobs per tick
			for i := 0; i < s.burst; i++ {
				processed, _ := s.svc.ProcessNext(localCtx)
				if !processed {
					break
				}
				processedCount++
			}
			s.mu.Lock()
			s.status.LastTick = time.Now()
			s.status.LastProcessed = processedCount
			s.mu.Unlock()
			metrics.SetSchedulerTick(s.status.LastTick)
		}
	}
}

// Stop cancels the scheduler's context, causing Start to return.
func (s *EncodingScheduler) Stop() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
}

// Pause temporarily stops the scheduler from picking up new jobs.
func (s *EncodingScheduler) Pause() {
	s.mu.Lock()
	s.paused = true
	s.mu.Unlock()
}

// Resume re-enables job processing after a Pause.
func (s *EncodingScheduler) Resume() {
	s.mu.Lock()
	s.paused = false
	s.mu.Unlock()
}

// IsPaused reports whether the scheduler is currently paused.
func (s *EncodingScheduler) IsPaused() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.paused
}
