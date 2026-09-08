package uploadfinalize

import (
	"context"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/upload"
	"github.com/vidra/vidra-core/internal/video"
)

// A28's first ruling-shaped finding: a malware rejection was invisible to the
// creator. The upload session settled `state: completed, failure_reason: ""`
// and the Studio showed a bare FAILED badge, beside a `quarantined` video that
// got a whole explanatory sentence. The vocabulary existed; the malware path had
// nothing in it.
//
// These are the RED-first assertions on the session state, which is the only
// place a polling client can read the outcome.

// TestMalwareRejectionFailsTheSessionWithTheNeutralSentence.
func TestMalwareRejectionFailsTheSessionWithTheNeutralSentence(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.pipeline.processErr = &video.MalwareRejectedError{Outcome: "infected"}
	sess := h.openAndFill(t, "ABCDEFGHIJ", upload.PurposeUpload)

	job, err := h.svc.Enqueue(ctx, sess.ID, h.video, upload.PurposeUpload, false)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := h.svc.DrainJobs(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}

	got := h.sessions.state(sess.ID)
	if got.State != upload.StateFailed {
		t.Errorf("session state = %q, want %q — a rejected upload that reads 'completed' tells the creator nothing",
			got.State, upload.StateFailed)
	}
	if got.FailureReason != video.SafetyScanRejectedMessage {
		t.Errorf("failure_reason = %q, want the neutral sentence %q", got.FailureReason, video.SafetyScanRejectedMessage)
	}
	// The verdict must not travel with it: naming the signature (or the
	// scanner) tells an attacker their probe worked.
	for _, leak := range []string{"malware", "infected", "clam", "signature"} {
		if strings.Contains(strings.ToLower(got.FailureReason), leak) {
			t.Errorf("failure_reason %q leaks %q", got.FailureReason, leak)
		}
	}
	if st := h.jobs.get(job.ID).State; st != StateFailed {
		t.Errorf("job state = %q, want failed", st)
	}
}

// TestMalwareRejectionDoesNotRetry. The verdict will not change, and while the
// ladder runs the session sits in `processing` — the creator watches a spinner
// for fifteen minutes and is then told nothing useful.
func TestMalwareRejectionDoesNotRetry(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.pipeline.processErr = &video.MalwareRejectedError{Outcome: "infected"}
	sess := h.openAndFill(t, "ABCDEFGHIJ", upload.PurposeUpload)

	job, err := h.svc.Enqueue(ctx, sess.ID, h.video, upload.PurposeUpload, false)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := h.svc.DrainJobs(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	row := h.jobs.get(job.ID)
	if row.State != StateFailed {
		t.Fatalf("job state = %q after ONE attempt, want failed (dead-lettered immediately)", row.State)
	}
	if row.Attempts > 1 {
		t.Errorf("attempts = %d, want 1 — a scanner verdict must not walk the backoff ladder", row.Attempts)
	}
}

// TestOrdinaryFailuresStillRetry keeps the terminal path narrow: a transient
// pipeline error must still get its four extra attempts.
func TestOrdinaryFailuresStillRetry(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.pipeline.processErr = context.DeadlineExceeded
	sess := h.openAndFill(t, "ABCDEFGHIJ", upload.PurposeUpload)

	job, err := h.svc.Enqueue(ctx, sess.ID, h.video, upload.PurposeUpload, false)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := h.svc.DrainJobs(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if st := h.jobs.get(job.ID).State; st == StateFailed {
		t.Error("a transient pipeline error dead-lettered on attempt 1")
	}
	if got := h.sessions.state(sess.ID).State; got != upload.StateProcessing {
		t.Errorf("session state = %q, want processing between attempts", got)
	}
}
