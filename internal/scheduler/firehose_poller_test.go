package scheduler

import (
	"context"
	"testing"
	"time"
)

func TestNewFirehosePoller(t *testing.T) {
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
			name:             "zero interval defaults to 5s",
			interval:         0,
			burst:            2,
			expectedInterval: 5 * time.Second,
			expectedBurst:    2,
		},
		{
			name:             "negative interval defaults to 5s",
			interval:         -1 * time.Second,
			burst:            2,
			expectedInterval: 5 * time.Second,
			expectedBurst:    2,
		},
		{
			name:             "zero burst defaults to 3",
			interval:         100 * time.Millisecond,
			burst:            0,
			expectedInterval: 100 * time.Millisecond,
			expectedBurst:    3,
		},
		{
			name:             "negative burst defaults to 3",
			interval:         100 * time.Millisecond,
			burst:            -5,
			expectedInterval: 100 * time.Millisecond,
			expectedBurst:    3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			poller := NewFirehosePoller(svc, tt.interval, tt.burst)

			if poller == nil {
				t.Fatal("Expected poller to be non-nil")
			}

			if poller.interval != tt.expectedInterval {
				t.Errorf("Expected interval %v, got %v", tt.expectedInterval, poller.interval)
			}

			if poller.burst != tt.expectedBurst {
				t.Errorf("Expected burst %d, got %d", tt.expectedBurst, poller.burst)
			}
		})
	}
}

func TestFirehosePoller_StartStop(t *testing.T) {
	svc := &mockFederationService{shouldSucceed: true}
	poller := NewFirehosePoller(svc, 50*time.Millisecond, 2)

	ctx := context.Background()

	// Start poller
	go poller.Start(ctx)

	// Wait for a few ticks
	time.Sleep(200 * time.Millisecond)

	// Stop poller
	poller.Stop()

	// Verify some items were processed
	count := svc.GetProcessedCount()
	if count == 0 {
		t.Error("Expected at least some items to be processed")
	}
}

func TestFirehosePoller_NoWorkAvailable(t *testing.T) {
	svc := &mockFederationService{shouldSucceed: false}
	poller := NewFirehosePoller(svc, 50*time.Millisecond, 5)

	ctx := context.Background()

	// Start poller
	go poller.Start(ctx)

	// Wait for a few ticks
	time.Sleep(200 * time.Millisecond)

	// Stop poller
	poller.Stop()

	// Verify no items were processed
	count := svc.GetProcessedCount()
	if count != 0 {
		t.Errorf("Expected no items to be processed, got %d", count)
	}
}

func TestFirehosePoller_ContextCancellation(t *testing.T) {
	svc := &mockFederationService{shouldSucceed: true}
	poller := NewFirehosePoller(svc, 50*time.Millisecond, 2)

	ctx, cancel := context.WithCancel(context.Background())

	// Start poller
	done := make(chan struct{})
	go func() {
		poller.Start(ctx)
		close(done)
	}()

	// Wait for processing
	time.Sleep(150 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait for poller to stop
	select {
	case <-done:
		// Good, poller stopped
	case <-time.After(1 * time.Second):
		t.Error("Poller did not stop after context cancellation")
	}
}

func TestFirehosePoller_BurstProcessing(t *testing.T) {
	svc := &mockFederationService{shouldSucceed: true}
	burst := 3
	poller := NewFirehosePoller(svc, 100*time.Millisecond, burst)

	ctx := context.Background()

	// Start poller
	go poller.Start(ctx)

	// Wait for at least one tick
	time.Sleep(150 * time.Millisecond)

	// Stop poller
	poller.Stop()

	// Should have processed at least burst items
	count := svc.GetProcessedCount()
	if count < burst {
		t.Errorf("Expected at least %d items processed, got %d", burst, count)
	}
}

func TestFirehosePoller_MultipleStopCalls(t *testing.T) {
	svc := &mockFederationService{shouldSucceed: true}
	poller := NewFirehosePoller(svc, 50*time.Millisecond, 2)

	ctx := context.Background()

	// Start poller
	go poller.Start(ctx)

	// Wait briefly
	time.Sleep(100 * time.Millisecond)

	// Multiple stop calls should not panic
	poller.Stop()
	poller.Stop()
	poller.Stop()
}

func TestFirehosePoller_RapidTickRate(t *testing.T) {
	svc := &mockFederationService{shouldSucceed: true}
	// Very short interval to test rapid polling
	poller := NewFirehosePoller(svc, 10*time.Millisecond, 1)

	ctx := context.Background()

	// Start poller
	go poller.Start(ctx)

	// Wait for multiple rapid ticks
	time.Sleep(100 * time.Millisecond)

	// Stop poller
	poller.Stop()

	// Should have processed many items due to rapid ticking
	count := svc.GetProcessedCount()
	if count < 5 {
		t.Errorf("Expected at least 5 items processed with rapid ticking, got %d", count)
	}
}
