package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"athena/internal/usecase/encoding"
)

// fakeEncodingService implements encoding.Service for testing.
type fakeEncodingService struct {
	calls int32
}

func (f *fakeEncodingService) Run(ctx context.Context, workers int) error { return nil }

func (f *fakeEncodingService) ProcessNext(ctx context.Context) (bool, error) {
	// First call returns processed=true, following calls return false
	if atomic.AddInt32(&f.calls, 1) == 1 {
		return true, nil
	}
	return false, nil
}

var _ encoding.Service = (*fakeEncodingService)(nil)

func TestEncodingSchedulerProcessesJobs(t *testing.T) {
	f := &fakeEncodingService{}
	sched := NewEncodingScheduler(f, 10*time.Millisecond, 2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sched.Start(ctx)
	// Allow a few ticks
	time.Sleep(50 * time.Millisecond)
	cancel()
	// small grace
	time.Sleep(10 * time.Millisecond)

	if atomic.LoadInt32(&f.calls) == 0 {
		t.Fatalf("expected scheduler to invoke ProcessNext at least once")
	}
}
