package video

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// mustProcess runs Process and tolerates exactly one error: the terminal
// safety-scan rejection Process now returns ALONGSIDE the persisted 'failed'
// row. Everything else is still a test failure.
//
// The rejection is an error rather than a state the caller has to interpret
// because that is the only shape the two async pipelines could act on: they see
// `err`, and before this change a malware verdict was indistinguishable from
// "no error at all", which is how a rejected upload settled its session as
// `completed` with an empty failure_reason (A28).
func mustProcess(t *testing.T, svc *Service, ctx context.Context, id uuid.UUID, key string) sqlcgen.Video {
	t.Helper()
	got, err := svc.Process(ctx, id, key)
	var rejected *MalwareRejectedError
	if err != nil && !errors.As(err, &rejected) {
		t.Fatalf("Process: %v", err)
	}
	return got
}

// TestProcessReportsMalwareRejectionAsTerminal pins the contract the pipelines
// depend on: a scanner refusal comes back as a *MalwareRejectedError that
// declares itself terminal, carries the audit outcome class, and arrives WITH
// the persisted 'failed' row rather than instead of it.
func TestProcessReportsMalwareRejectionAsTerminal(t *testing.T) {
	for _, tc := range []struct {
		name        string
		scanner     Scanner
		mode        string
		wantOutcome string
	}{
		{"infected", fakeScanner{clean: false}, "fail-closed", "infected"},
		{"infected under fail-open", fakeScanner{clean: false}, "fail-open", "infected"},
		{"unscannable under fail-closed", fakeScanner{err: errors.New("clamd down")}, "fail-closed", "scan_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(newFakeRepo(uuid.New()), nil, WithScanner(tc.scanner), WithScanMode(tc.mode))
			ctx := context.Background()
			v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
			got, err := svc.Process(ctx, v.ID, "k")
			var rejected *MalwareRejectedError
			if !errors.As(err, &rejected) {
				t.Fatalf("Process error = %v, want *MalwareRejectedError", err)
			}
			if rejected.Outcome != tc.wantOutcome {
				t.Errorf("outcome = %q, want %q", rejected.Outcome, tc.wantOutcome)
			}
			if !rejected.Terminal() {
				t.Error("rejection is not terminal; the pipelines would retry a verdict that cannot change")
			}
			if got.State != "failed" {
				t.Errorf("state = %q, want failed — the row must be persisted alongside the error", got.State)
			}
		})
	}
}

// TestProcessDoesNotRejectWhenPolicyAbsorbs is the other half: quarantine and
// fail-open are NOT refusals, so they must not come back as errors — a job that
// treated them as one would dead-letter a video it had just published.
func TestProcessDoesNotRejectWhenPolicyAbsorbs(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      string
		wantState string
	}{
		{"unscannable under fail-open publishes", "fail-open", "published"},
		{"unscannable under quarantine parks", "quarantine", "quarantined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(newFakeRepo(uuid.New()), nil,
				WithScanner(fakeScanner{err: errors.New("clamd down")}), WithScanMode(tc.mode))
			ctx := context.Background()
			v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
			got, err := svc.Process(ctx, v.ID, "k")
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if got.State != tc.wantState {
				t.Errorf("state = %q, want %q", got.State, tc.wantState)
			}
		})
	}
}

// TestSafetyScanCopyIsNeutral pins the sentence itself. It is the creator-facing
// contract and the frontend renders it from the code, so a drifting word here
// is a product change, not a refactor.
func TestSafetyScanCopyIsNeutral(t *testing.T) {
	const want = "This file was rejected by the instance's safety scan and was not stored."
	if SafetyScanRejectedMessage != want {
		t.Errorf("SafetyScanRejectedMessage = %q, want %q", SafetyScanRejectedMessage, want)
	}
	if SafetyScanRejectedCode != "safety_scan_rejected" {
		t.Errorf("SafetyScanRejectedCode = %q", SafetyScanRejectedCode)
	}
	// The sentence must name neither the engine nor the verdict: telling a
	// creator "malware" tells an attacker their probe worked.
	for _, banned := range []string{"malware", "virus", "clam", "signature", "infected"} {
		if containsFold(SafetyScanRejectedMessage, banned) {
			t.Errorf("the creator sentence names %q: %q", banned, SafetyScanRejectedMessage)
		}
	}
}

func containsFold(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexFold(s, sub) >= 0
}

func indexFold(s, sub string) int {
	lower := func(b byte) byte {
		if b >= 'A' && b <= 'Z' {
			return b + 32
		}
		return b
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		ok := true
		for j := 0; j < len(sub); j++ {
			if lower(s[i+j]) != lower(sub[j]) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}
