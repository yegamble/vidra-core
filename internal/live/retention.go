package live

import (
	"context"
	"time"
)

// Session-recording retention — the LIVE_RECORDING_RETENTION half that the
// delete-on-publish in RunReplay does not cover.
//
// THE TWO MODES, and why they are not the same sweep:
//
//	0 (default)  The recording is an intermediate. It is deleted the instant its
//	             replay VOD is published (RunReplay), and this sweep does
//	             NOTHING. There is no window to enforce, and a sweep that
//	             invented one would be deleting recordings the operator never
//	             asked it to.
//	N > 0        The operator wants the originals kept for N so a replay can be
//	             re-transcoded from source. Nothing is deleted on publish, and
//	             this sweep takes everything older than N — including the two
//	             kinds of recording the publish path can never reach: a stream
//	             with replay_enabled = false (nginx-rtmp records unconditionally,
//	             so those files exist and nothing ever republishes them) and a
//	             replay whose transcode failed.
//
// The honest consequence of the default, stated here because it is the one
// thing an operator can be surprised by: at retention 0 those same two kinds of
// recording are never deleted by anything. That is the pre-existing behaviour
// A26 measured, unchanged — this key gives an operator a way OUT of it, and a
// non-zero value is the way. It is documented in docs/operations.md and in the
// env templates rather than papered over with a hidden floor, because a sweep
// that deletes files at a threshold nobody configured is how a "the only copy"
// recording disappears.

// recordingPruneBatch is how many recordings one pass deletes, matching the
// bounded-batch convention of the QoE and audit prunes it runs beside. 200 files
// is a few seconds of unlinks in the worst case and drains a year of accumulated
// sessions within a day of hourly passes.
const recordingPruneBatch = 200

// PruneRecordings deletes session recordings that have outlived
// LIVE_RECORDING_RETENTION and returns how many went.
//
// A no-op (0, nil) when retention is 0 — see the mode note above — and when no
// recording store is wired. `now` is a parameter so the pass is testable without
// a clock, exactly as qoe.Prune and audit.Prune are.
func (s *Service) PruneRecordings(_ context.Context, now time.Time) (int, error) {
	retention := s.recordingRetention()
	if retention == 0 || s.recordings == nil {
		return 0, nil
	}
	return s.recordings.PruneRecordings(now.Add(-retention), recordingPruneBatch)
}
