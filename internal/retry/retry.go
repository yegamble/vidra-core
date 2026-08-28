// Package retry holds the scheduling arithmetic behind the workers'
// retry-or-dead-letter loops.
//
// Every queue-backed worker (transcode, video import, caption jobs, channel
// sync, federation delivery, ATProto posting, search-event drain, account
// export, storage migration, IPFS mirror) reschedules a failed attempt with the
// same exponential backoff. It used to be nine hand-written copies of one loop
// plus a tenth spelling as a shift, which is exactly the kind of arithmetic that
// silently drifts: these delays decide how long a job keeps retrying before it
// dead-letters, so a doubled cap in one worker is an outage nobody notices for a
// day. One definition, one table test.
package retry

import "time"

// Backoff is the delay before retry attempt n (1-based, so attempt 1 — the first
// retry after the first failure — waits base): base doubled once per attempt
// already spent, capped at max.
//
// The cap is applied as the doubling runs, so the returned delay is never above
// max EXCEPT when base itself already exceeds it: Backoff(1, 2*time.Hour,
// time.Hour) is two hours. That is the pre-existing behaviour of every call site
// (all of which configure base well under max) and is preserved deliberately.
//
// A non-positive attempt returns base, matching the loop this replaces.
func Backoff(attempt int, base, max time.Duration) time.Duration {
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= max {
			return max
		}
	}
	return d
}
