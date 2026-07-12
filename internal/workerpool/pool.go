// Package workerpool is the small bounded-concurrency primitive the durable
// job-queue workers share (config-parity W10). The transcode and video-import
// services run each claimed batch through Run with a concurrency value read
// from the instance-settings overlay per drain call, so an admin can retune
// worker parallelism without a restart. Concurrency 1 degenerates to a plain
// sequential loop — exactly the pre-W10 behavior — keeping job ordering fully
// deterministic at the default.
package workerpool

import "sync"

// MaxConcurrency is the upper bound Clamp enforces (mirrors the registry
// validators for transcoding_concurrency / import_jobs_concurrency).
const MaxConcurrency = 16

// Clamp bounds a runtime concurrency value into [1, MaxConcurrency] so a
// defensive read can never stall (0/negative) or stampede (absurdly large) a
// worker. The registry validator refuses out-of-range writes; this guards
// direct DB edits and unwired providers.
func Clamp(n int64) int {
	if n < 1 {
		return 1
	}
	if n > MaxConcurrency {
		return MaxConcurrency
	}
	return int(n)
}

// Run invokes fn(i) for every i in [0, n) using at most workers concurrent
// goroutines, and returns once all invocations finish. workers <= 1 runs
// sequentially on the caller's goroutine (deterministic order); otherwise
// indexes are dispatched in order to min(workers, n) goroutines, so claim
// order is preserved at dispatch even though completions may interleave. fn is
// responsible for its own error/context handling (job workers persist per-job
// outcomes; nothing is surfaced here).
func Run(workers, n int, fn func(i int)) {
	if n <= 0 {
		return
	}
	if workers <= 1 {
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}
	if workers > n {
		workers = n
	}
	idx := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := range idx {
				fn(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		idx <- i
	}
	close(idx)
	wg.Wait()
}
