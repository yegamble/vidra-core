package peertubeimport

import "testing"

// The legacy heal reads a terminal ledger row as unsettled, so its boundary is a
// safety property: a note outside the six an older release wrote for a missing
// parent must stay terminal, or a deletion made on this instance — a retired
// mapping — is undone by the next run.
func TestLegacyParentWaitBoundary(t *testing.T) {
	for note := range legacyParentMissingNotes {
		if !legacyParentWait("skipped", false, note) {
			t.Errorf("legacy note %q must be re-evaluated", note)
		}
		if legacyParentWait("skipped", true, note) {
			t.Errorf("%q with a target is a LINK (conflict policy), not a wait", note)
		}
		if legacyParentWait("done", false, note) || legacyParentWait("unsupported", false, note) {
			t.Errorf("%q on a non-skipped row must stay terminal", note)
		}
	}
	if len(legacyParentMissingNotes) != 6 {
		t.Errorf("legacy notes = %d, want exactly the six an older release wrote", len(legacyParentMissingNotes))
	}
	for _, note := range []string{
		deletedParentNote, "empty body", "username or email already exists",
		"channel handle already exists", "the source no longer has this subscription", "",
	} {
		if legacyParentWait("skipped", false, note) {
			t.Errorf("note %q must stay terminal", note)
		}
	}
}

func TestNoteWaitingSaysWhatTheLedgerNoLongerDoes(t *testing.T) {
	r := NewReport(false, PolicySkip, false)
	_ = awaitParent(r.count(KindChannel))
	_ = awaitParent(r.count(KindComment))
	_ = awaitParent(r.count(KindComment))
	r.count(KindVideo).Skipped = 9 // already imported: not a wait, not a note
	r.noteWaiting()
	if len(r.Conflicts) != 2 {
		t.Fatalf("conflicts = %q, want one note per waiting kind", r.Conflicts)
	}
	if got := r.Entities[KindComment]; got.Skipped != 2 || got.waiting != 2 {
		t.Errorf("comment counts = %+v, want 2 skipped / 2 waiting", got)
	}
}
