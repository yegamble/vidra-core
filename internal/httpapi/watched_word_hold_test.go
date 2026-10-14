package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestWatchedWordHoldListUsesTheExistingHoldPath: a comment matching a "hold"
// list is stored through the same held-for-review path a requires_approval video
// uses — visible to its author and the video's managers only — on a video whose
// policy would otherwise publish it. Managers are exempt; a plain "flag" match
// still publishes; an edit that newly introduces a hold term re-holds. Every
// case is flagged either way.
func TestWatchedWordHoldListUsesTheExistingHoldPath(t *testing.T) {
	c := newApprovalCast(t)
	vid := createPublishedVideo(t, c.srv, c.bob, "bob", `{"title":"open","privacy":"public"}`)
	comments := "/api/v1/videos/" + vid + "/comments"
	for _, w := range []string{"hold-me", "just-flag"} {
		if rec := sendJSONAuth(c.srv, http.MethodPost, "/api/v1/admin/watched-words", `{"word":"`+w+`"}`, c.ada); rec.Code != http.StatusCreated {
			t.Fatalf("add %s = %d", w, rec.Code)
		}
	}
	post := func(tok, body string) commentView {
		t.Helper()
		rec := sendJSONAuth(c.srv, http.MethodPost, comments, `{"body":"`+body+`"}`, tok)
		if rec.Code != http.StatusCreated {
			t.Fatalf("post %q = %d; body=%s", body, rec.Code, rec.Body.String())
		}
		var cv commentView
		_ = json.Unmarshal(rec.Body.Bytes(), &cv)
		return cv
	}

	if cv := post(c.eve, "please hold-me now"); !cv.HeldForReview {
		t.Errorf("hold-list match by a stranger: held_for_review = false, want true")
	}
	if cv := post(c.eve, "only just-flag here"); cv.HeldForReview {
		t.Errorf("flag-list match was held; flag must keep today's behaviour (publish)")
	}
	if cv := post(c.bob, "owner says hold-me"); cv.HeldForReview {
		t.Errorf("the video's owner must be exempt from a hold list")
	}

	// The held comment is hidden from other viewers, visible to the owner.
	list := func(tok string) int {
		var out struct {
			Total int `json:"total"`
		}
		_ = json.Unmarshal(getWithAuth(c.srv, comments, tok).Body.Bytes(), &out)
		return out.Total
	}
	if got := list(c.carl); got != 2 { // flag-only + owner's; the held one is not counted
		t.Errorf("stranger sees %d comments, want 2", got)
	}
	if got := list(c.bob); got != 3 {
		t.Errorf("owner sees %d comments, want 3 (held one included)", got)
	}

	// An edit that newly introduces a hold term is re-held.
	clean := post(c.carl, "totally fine")
	rec := sendJSONAuth(c.srv, http.MethodPatch, "/api/v1/comments/"+clean.ID, `{"body":"now hold-me"}`, c.carl)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit = %d; body=%s", rec.Code, rec.Body.String())
	}
	var edited commentView
	_ = json.Unmarshal(rec.Body.Bytes(), &edited)
	if !edited.HeldForReview {
		t.Errorf("edit introducing a hold term: held_for_review = false, want true")
	}

	// Held or not, all of them were flagged for the moderators' queue.
	var q watchedWordMatchListResponse
	_ = json.Unmarshal(getWithAuth(c.srv, "/api/v1/admin/watched-word-matches", c.ada).Body.Bytes(), &q)
	if len(q.Matches) != 4 {
		t.Errorf("flagged matches = %d, want 4", len(q.Matches))
	}
}
