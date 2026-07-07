package video

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
)

// recordingAuditor captures the audit events Process emits so the malware-
// rejection trail can be asserted without a database.
type recordingAuditor struct{ events []audit.Event }

func (r *recordingAuditor) Record(_ context.Context, ev audit.Event) error {
	r.events = append(r.events, ev)
	return nil
}

// process runs a scan through a fresh service wired with the recording auditor
// and the given scanner + mode, returning the resulting state and the auditor.
func processWithAuditor(t *testing.T, sc Scanner, mode string) (string, *recordingAuditor) {
	t.Helper()
	aud := &recordingAuditor{}
	opts := []Option{WithScanner(sc), WithAuditor(aud)}
	if mode != "" {
		opts = append(opts, WithScanMode(mode))
	}
	svc := NewService(newFakeRepo(uuid.New()), nil, opts...)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "k")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return got.State, aud
}

func TestProcessAuditsMalwareRejections(t *testing.T) {
	tests := []struct {
		name        string
		scanner     Scanner
		mode        string
		wantState   string
		wantAudited bool
		wantOutcome string
	}{
		{"infected fail-closed", fakeScanner{clean: false}, "fail-closed", "failed", true, "infected"},
		{"infected fail-open", fakeScanner{clean: false}, "fail-open", "failed", true, "infected"},
		{"infected quarantine", fakeScanner{clean: false}, "quarantine", "failed", true, "infected"},
		{"scan-error fail-closed", fakeScanner{err: errors.New("clamd down")}, "fail-closed", "failed", true, "scan_error"},
		{"scan-error quarantine", fakeScanner{err: errors.New("clamd down")}, "quarantine", "quarantined", true, "scan_error"},
		// fail-open publishes → not a rejection → no rejection audit event.
		{"scan-error fail-open publishes", fakeScanner{err: errors.New("clamd down")}, "fail-open", "published", false, ""},
		// A clean scan never audits.
		{"clean publishes", fakeScanner{clean: true}, "fail-closed", "published", false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state, aud := processWithAuditor(t, tc.scanner, tc.mode)
			if state != tc.wantState {
				t.Errorf("state = %q, want %q", state, tc.wantState)
			}
			if !tc.wantAudited {
				if len(aud.events) != 0 {
					t.Fatalf("got %d audit events, want 0: %+v", len(aud.events), aud.events)
				}
				return
			}
			if len(aud.events) != 1 {
				t.Fatalf("got %d audit events, want 1: %+v", len(aud.events), aud.events)
			}
			ev := aud.events[0]
			if ev.Action != observability.ActionUploadMalwareRejected {
				t.Errorf("action = %q, want %q", ev.Action, observability.ActionUploadMalwareRejected)
			}
			if ev.Result != observability.ResultFailure {
				t.Errorf("result = %q, want %q", ev.Result, observability.ResultFailure)
			}
			if !strings.Contains(ev.Reason, "outcome="+tc.wantOutcome) {
				t.Errorf("reason = %q, want it to contain outcome=%s", ev.Reason, tc.wantOutcome)
			}
			// The reason must never leak file content — only safe metadata.
			if !strings.Contains(ev.Reason, "video=") || !strings.Contains(ev.Reason, "policy=") {
				t.Errorf("reason = %q, want video= and policy= metadata", ev.Reason)
			}
		})
	}
}

// TestProcessMalwareRejectionAuditIsOptional proves the state transition still
// holds when no auditor is wired (the audit event is simply skipped).
func TestProcessMalwareRejectionAuditIsOptional(t *testing.T) {
	svc := NewService(newFakeRepo(uuid.New()), nil, WithScanner(fakeScanner{clean: false}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "k")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.State != "failed" {
		t.Errorf("state = %q, want failed (infected, no auditor)", got.State)
	}
}
