package workerpool

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestClamp(t *testing.T) {
	cases := map[int64]int{
		-5: 1, 0: 1, 1: 1, 2: 2, 16: 16, 17: 16, 1 << 40: 16,
	}
	for in, want := range cases {
		if got := Clamp(in); got != want {
			t.Errorf("Clamp(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestRunSequentialAtOne(t *testing.T) {
	// workers <= 1 must run in exact index order on the caller's goroutine —
	// the pre-W10 job-loop behavior.
	var order []int
	Run(1, 5, func(i int) { order = append(order, i) })
	for i, got := range order {
		if got != i {
			t.Fatalf("order = %v, want strictly sequential", order)
		}
	}
	if len(order) != 5 {
		t.Fatalf("ran %d, want 5", len(order))
	}
}

func TestRunAllIndexesOnceBounded(t *testing.T) {
	const n, workers = 40, 4
	var (
		mu      sync.Mutex
		seen    = map[int]int{}
		active  atomic.Int64
		maxSeen atomic.Int64
	)
	gate := make(chan struct{}, n)
	Run(workers, n, func(i int) {
		cur := active.Add(1)
		for {
			prev := maxSeen.Load()
			if cur <= prev || maxSeen.CompareAndSwap(prev, cur) {
				break
			}
		}
		gate <- struct{}{}
		mu.Lock()
		seen[i]++
		mu.Unlock()
		active.Add(-1)
	})
	if len(seen) != n {
		t.Fatalf("ran %d distinct indexes, want %d", len(seen), n)
	}
	for i, c := range seen {
		if c != 1 {
			t.Errorf("index %d ran %d times, want exactly once", i, c)
		}
	}
	if maxSeen.Load() > workers {
		t.Errorf("observed %d concurrent invocations, bound is %d", maxSeen.Load(), workers)
	}
}

func TestRunZeroAndWorkersAboveN(t *testing.T) {
	Run(4, 0, func(int) { t.Fatal("fn must not run for n=0") })
	var count atomic.Int64
	Run(16, 3, func(int) { count.Add(1) })
	if count.Load() != 3 {
		t.Fatalf("ran %d, want 3", count.Load())
	}
}
