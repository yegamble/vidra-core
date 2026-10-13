package ipfsmirror

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNoteAdmissionReasonLogsOnlyTransitions(t *testing.T) {
	var buf bytes.Buffer
	s := &Service{logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	used := int64(900)
	lines := func() []string {
		out := strings.Split(strings.TrimSpace(buf.String()), "\n")
		if len(out) == 1 && out[0] == "" {
			return nil
		}
		return out
	}

	s.noteAdmissionReason("", 1000, &used, 0, nil)
	if len(lines()) != 0 {
		t.Fatalf("admitting from the start logged: %v", lines())
	}
	s.noteAdmissionReason("budget_exhausted", 1000, &used, 100, nil)
	s.noteAdmissionReason("budget_exhausted", 1000, &used, 100, nil) // same pass again: silent
	got := lines()
	if len(got) != 1 || !strings.Contains(got[0], "level=WARN") || !strings.Contains(got[0], "ipfs_admission_paused") || !strings.Contains(got[0], "reason=budget_exhausted") || !strings.Contains(got[0], "repo_used_bytes=900") {
		t.Fatalf("pause transition = %v", got)
	}
	s.noteAdmissionReason("publication_paused", 1000, &used, 0, nil) // operator switch: info, not a warning
	got = lines()
	if len(got) != 2 || strings.Contains(got[1], "level=WARN") || !strings.Contains(got[1], "previous_reason=budget_exhausted") {
		t.Fatalf("reason change = %v", got)
	}
	s.noteAdmissionReason("", 1000, &used, 0, nil)
	got = lines()
	if len(got) != 3 || !strings.Contains(got[2], "ipfs_admission_resumed") {
		t.Fatalf("resume = %v", got)
	}
}
