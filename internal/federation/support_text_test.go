package federation

import (
	"encoding/json"
	"testing"
)

// PeerTube carries the free-text "support" field on both Video and Group
// objects. Outbound only: a remote video has no column to store it in.
func TestVideoObjectCarriesSupportText(t *testing.T) {
	repo := newContractRepo()
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	row := repo.videosByID[ctVideoID]

	emit := func() map[string]any {
		t.Helper()
		payload, err := svc.buildVideoActivity(t.Context(), "Create", "films", row)
		if err != nil {
			t.Fatalf("buildVideoActivity: %v", err)
		}
		var act struct {
			Object map[string]any `json:"object"`
		}
		if err := json.Unmarshal(payload, &act); err != nil {
			t.Fatal(err)
		}
		return act.Object
	}

	if _, has := emit()["support"]; has {
		t.Fatal("a video with no support text emitted a support key")
	}
	text := "Support me on Liberapay"
	row.Support = &text
	repo.videosByID[ctVideoID] = row
	if got := emit()["support"]; got != text {
		t.Fatalf("support = %v, want %q", got, text)
	}
	// Below PeerTube's own 3-character minimum the field is left off rather
	// than risking the whole object for an optional field.
	short := "ab"
	row.Support = &short
	repo.videosByID[ctVideoID] = row
	if _, has := emit()["support"]; has {
		t.Fatal("a 2-character support text was emitted; PeerTube's minimum is 3")
	}
}

func TestFederatedSupportBounds(t *testing.T) {
	long := make([]rune, 1001)
	for i := range long {
		long[i] = 'x'
	}
	ls := string(long)
	ok := "abc"
	for name, tc := range map[string]struct {
		in   *string
		want string
	}{"nil": {nil, ""}, "ok": {&ok, "abc"}, "too long": {&ls, ""}} {
		if got := federatedSupport(tc.in); got != tc.want {
			t.Errorf("%s: federatedSupport = %q, want %q", name, got, tc.want)
		}
	}
}
