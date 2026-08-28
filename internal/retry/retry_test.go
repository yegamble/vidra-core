package retry

import (
	"testing"
	"time"
)

// TestBackoffPinsEveryCallerSchedule is the safety net for folding nine
// hand-written backoff loops into one function: it pins the exact delay every
// existing caller's (base, max) pair produces for attempts 1..8, computed from
// the code as it stood before the consolidation. A change to Backoff that moves
// any worker's retry-vs-dead-letter timing has to break this table first.
func TestBackoffPinsEveryCallerSchedule(t *testing.T) {
	m, h := time.Minute, time.Hour
	cases := []struct {
		name string
		base time.Duration
		max  time.Duration
		want []time.Duration // attempts 1..8
	}{
		{
			// transcode, captionjob, videoimport, account/export, storagemigration.
			name: "minute base, hour cap",
			base: m, max: h,
			want: []time.Duration{m, 2 * m, 4 * m, 8 * m, 16 * m, 32 * m, h, h},
		},
		{
			// searchevents drainer (MASTER-PLAN §2.3: 30s→1h).
			name: "thirty second base, hour cap",
			base: 30 * time.Second, max: h,
			want: []time.Duration{30 * time.Second, m, 2 * m, 4 * m, 8 * m, 16 * m, 32 * m, h},
		},
		{
			// federation delivery and atproto posting.
			name: "thirty second base, six hour cap",
			base: 30 * time.Second, max: 6 * h,
			want: []time.Duration{30 * time.Second, m, 2 * m, 4 * m, 8 * m, 16 * m, 32 * m, 64 * m},
		},
		{
			// ipfsmirror's base is configurable (default a minute, covered above);
			// this pins a short operator-chosen one, where the cap never bites.
			name: "ten second base, hour cap",
			base: 10 * time.Second, max: h,
			want: []time.Duration{
				10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second,
				160 * time.Second, 320 * time.Second, 640 * time.Second, 1280 * time.Second,
			},
		},
		{
			// ipfsmirror configured with no delay (tests re-claim immediately).
			name: "zero base never grows",
			base: 0, max: h,
			want: []time.Duration{0, 0, 0, 0, 0, 0, 0, 0},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i, want := range tc.want {
				attempt := i + 1
				if got := Backoff(attempt, tc.base, tc.max); got != want {
					t.Errorf("Backoff(%d, %v, %v) = %v, want %v", attempt, tc.base, tc.max, got, want)
				}
			}
		})
	}
}

// TestBackoffMatchesStorageMigrationShift proves the one call site that did NOT
// spell the schedule as a doubling loop — storagemigration computed
// `min(time.Minute << min(attempt-1, 6), time.Hour)` — lands on the identical
// delay for every attempt it can be called with, so switching it to Backoff
// cannot move a migration campaign's retry timing.
func TestBackoffMatchesStorageMigrationShift(t *testing.T) {
	old := func(attempt int) time.Duration {
		return min(time.Minute<<min(attempt-1, 6), time.Hour)
	}
	for attempt := 1; attempt <= 16; attempt++ {
		if got, want := Backoff(attempt, time.Minute, time.Hour), old(attempt); got != want {
			t.Errorf("attempt %d: Backoff = %v, storagemigration shift = %v", attempt, got, want)
		}
	}
}

// TestBackoffNonPositiveAttemptReturnsBase pins the degenerate input the loops
// accepted silently (the doubling simply never ran). Worth keeping explicit: the
// shift form storagemigration used would have panicked here instead.
func TestBackoffNonPositiveAttemptReturnsBase(t *testing.T) {
	for _, attempt := range []int{0, -1} {
		if got := Backoff(attempt, time.Minute, time.Hour); got != time.Minute {
			t.Errorf("Backoff(%d, 1m, 1h) = %v, want 1m", attempt, got)
		}
	}
}

// TestBackoffBaseAboveCapIsNotClamped pins the surprising-but-original edge: the
// cap is only consulted after a doubling, so a base already past max survives
// the first attempt untouched. No caller configures this; the test exists so a
// future "obvious" clamp is a deliberate change, not an accident.
func TestBackoffBaseAboveCapIsNotClamped(t *testing.T) {
	if got := Backoff(1, 2*time.Hour, time.Hour); got != 2*time.Hour {
		t.Errorf("Backoff(1, 2h, 1h) = %v, want 2h", got)
	}
	if got := Backoff(2, 2*time.Hour, time.Hour); got != time.Hour {
		t.Errorf("Backoff(2, 2h, 1h) = %v, want 1h", got)
	}
}
