package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"
)

type mockFederationService struct {
	mu             sync.Mutex
	processedCount int
	shouldSucceed  bool
}

func (m *mockFederationService) ProcessNext(ctx context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.shouldSucceed {
		return false, nil
	}
	m.processedCount++
	return true, nil
}

func (m *mockFederationService) GetProcessedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.processedCount
}

func TestNewFederationScheduler(t *testing.T) {
	svc := &mockFederationService{shouldSucceed: true}

	tests := []struct {
		name             string
		interval         time.Duration
		burst            int
		expectedInterval time.Duration
		expectedBurst    int
	}{
		{
			name:             "normal values",
			interval:         100 * time.Millisecond,
			burst:            5,
			expectedInterval: 100 * time.Millisecond,
			expectedBurst:    5,
		},
		{
			name:             "zero interval defaults to 10s",
			interval:         0,
			burst:            3,
			expectedInterval: 10 * time.Second,
			expectedBurst:    3,
		},
		{
			name:             "negative interval defaults to 10s",
			interval:         -1 * time.Second,
			burst:            3,
			expectedInterval: 10 * time.Second,
			expectedBurst:    3,
		},
		{
			name:             "zero burst defaults to 1",
			interval:         100 * time.Millisecond,
			burst:            0,
			expectedInterval: 100 * time.Millisecond,
			expectedBurst:    1,
		},
		{
			name:             "negative burst defaults to 1",
			interval:         100 * time.Millisecond,
			burst:            -5,
			expectedInterval: 100 * time.Millisecond,
			expectedBurst:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheduler := NewFederationScheduler(svc, tt.interval, tt.burst)

			if scheduler == nil {
				t.Fatal("Expected scheduler to be non-nil")
			}

			if scheduler.interval != tt.expectedInterval {
				t.Errorf("Expected interval %v, got %v", tt.expectedInterval, scheduler.interval)
			}

			if scheduler.burst != tt.expectedBurst {
				t.Errorf("Expected burst %d, got %d", tt.expectedBurst, scheduler.burst)
			}

			status := scheduler.Snapshot()
			if status.Burst != tt.expectedBurst {
				t.Errorf("Expected status burst %d, got %d", tt.expectedBurst, status.Burst)
			}
		})
	}
}

func TestFederationScheduler_StartStop(t *testing.T) {
	svc := &mockFederationService{shouldSucceed: true}
	scheduler := NewFederationScheduler(svc, 50*time.Millisecond, 2)

	ctx := context.Background()

	// Start scheduler
	go scheduler.Start(ctx)

	// Wait for a few ticks
	time.Sleep(200 * time.Millisecond)

	// Stop scheduler
	scheduler.Stop()

	// Verify some items were processed
	count := svc.GetProcessedCount()
	if count == 0 {
		t.Error("Expected at least some items to be processed")
	}
}

func TestFederationScheduler_Snapshot(t *testing.T) {
	svc := &mockFederationService{shouldSucceed: true}
	scheduler := NewFederationScheduler(svc, 50*time.Millisecond, 3)

	ctx := context.Background()

	// Start scheduler
	go scheduler.Start(ctx)

	// Wait for processing
	time.Sleep(150 * time.Millisecond)

	// Get snapshot
	status := scheduler.Snapshot()

	// Verify snapshot has data
	if status.Burst != 3 {
		t.Errorf("Expected burst 3, got %d", status.Burst)
	}

	if status.LastTick.IsZero() {
		t.Error("Expected LastTick to be set")
	}

	if status.LastProcessed == 0 {
		t.Error("Expected LastProcessed to be non-zero")
	}

	scheduler.Stop()
}

func TestFederationScheduler_NoWorkAvailable(t *testing.T) {
	svc := &mockFederationService{shouldSucceed: false}
	scheduler := NewFederationScheduler(svc, 50*time.Millisecond, 5)

	ctx := context.Background()

	// Start scheduler
	go scheduler.Start(ctx)

	// Wait for a few ticks
	time.Sleep(200 * time.Millisecond)

	// Stop scheduler
	scheduler.Stop()

	// Verify no items were processed
	count := svc.GetProcessedCount()
	if count != 0 {
		t.Errorf("Expected no items to be processed, got %d", count)
	}
}

func TestFederationScheduler_ContextCancellation(t *testing.T) {
	svc := &mockFederationService{shouldSucceed: true}
	scheduler := NewFederationScheduler(svc, 50*time.Millisecond, 2)

	ctx, cancel := context.WithCancel(context.Background())

	// Start scheduler
	done := make(chan struct{})
	go func() {
		scheduler.Start(ctx)
		close(done)
	}()

	// Wait for processing
	time.Sleep(150 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait for scheduler to stop
	select {
	case <-done:
		// Good, scheduler stopped
	case <-time.After(1 * time.Second):
		t.Error("Scheduler did not stop after context cancellation")
	}
}

func TestFederationScheduler_BurstLimit(t *testing.T) {
	svc := &mockFederationService{shouldSucceed: true}
	burst := 3
	scheduler := NewFederationScheduler(svc, 100*time.Millisecond, burst)

	ctx := context.Background()

	// Start scheduler
	go scheduler.Start(ctx)

	// Wait for exactly one tick
	time.Sleep(150 * time.Millisecond)

	scheduler.Stop()

	// Get snapshot to check last processed
	status := scheduler.Snapshot()

	// Should have processed exactly burst items in the last tick
	if status.LastProcessed != burst {
		t.Errorf("Expected LastProcessed to be %d, got %d", burst, status.LastProcessed)
	}
}
