package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestWatchedWordMatchesFlow(t *testing.T) {
	srv := videoServer(t)
	// The first registered account ("ada") becomes admin.
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)

	// The admin adds a watched word.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/watched-words", `{"word":"spam"}`, admin); rec.Code != http.StatusCreated {
		t.Fatalf("add watched word = %d; body=%s", rec.Code, rec.Body.String())
	}

	// A viewer posts a comment containing the watched term.
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	crec := postJSONAuth(srv, "/api/v1/videos/"+vid+"/comments", `{"body":"this is SPAM"}`, bob)
	if crec.Code != http.StatusCreated {
		t.Fatalf("comment = %d; body=%s", crec.Code, crec.Body.String())
	}
	var cv commentView
	_ = json.Unmarshal(crec.Body.Bytes(), &cv)

	// The flagged comment appears in the watched-word matches queue.
	rec := getWithAuth(srv, "/api/v1/admin/watched-word-matches", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("matches = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body watchedWordMatchListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Matches) != 1 || body.Matches[0].Word != "spam" || body.Matches[0].CommentID != cv.ID {
		t.Fatalf("matches = %+v, want one spam match for comment %s", body.Matches, cv.ID)
	}

	// A clean comment adds no new match.
	if rec := postJSONAuth(srv, "/api/v1/videos/"+vid+"/comments", `{"body":"nice video"}`, bob); rec.Code != http.StatusCreated {
		t.Fatalf("clean comment = %d", rec.Code)
	}
	var after watchedWordMatchListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/admin/watched-word-matches", admin).Body.Bytes(), &after)
	if len(after.Matches) != 1 {
		t.Errorf("after a clean comment, matches = %d, want still 1", len(after.Matches))
	}

	// A regular user cannot read the matches queue; anon is unauthorized.
	if rec := getWithAuth(srv, "/api/v1/admin/watched-word-matches", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-mod matches = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/watched-word-matches", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon matches = %d, want 401", rec.Code)
	}

	// Comment matches carry the "comment" type badge (§12 adds video ones).
	if body.Matches[0].Type != "comment" {
		t.Errorf("comment match type = %q, want comment", body.Matches[0].Type)
	}
}

// TestWatchedWordVideoMatchesFlow proves §12: a video's title+description are
// matched on create AND edit, recorded once per term+video, and surface in the
// shared review queue typed "video" with a title + owner context (no comment
// fields).
func TestWatchedWordVideoMatchesFlow(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/watched-words", `{"word":"spam"}`, admin); rec.Code != http.StatusCreated {
		t.Fatalf("add watched word = %d; body=%s", rec.Code, rec.Body.String())
	}

	// A creator (regular user) with a channel.
	bob := createChannelFor(t, srv, "bob", "bob@example.test", "bobtube")

	matches := func() []watchedWordMatchView {
		t.Helper()
		rec := getWithAuth(srv, "/api/v1/admin/watched-word-matches", admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("matches = %d; body=%s", rec.Code, rec.Body.String())
		}
		var body watchedWordMatchListResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body.Matches
	}

	// A clean video create flags nothing.
	clean := createVideo(t, srv, bob, "bobtube", `{"title":"wholesome cooking","privacy":"public"}`)
	if got := matches(); len(got) != 0 {
		t.Fatalf("clean create flagged %d matches, want 0", len(got))
	}

	// A create whose DESCRIPTION carries the term is flagged on create.
	flagged := createVideo(t, srv, bob, "bobtube", `{"title":"my mixtape","description":"pure SPAM inside","privacy":"public"}`)
	got := matches()
	if len(got) != 1 {
		t.Fatalf("create-flagged matches = %d, want 1", len(got))
	}
	m := got[0]
	if m.Type != "video" || m.Word != "spam" || m.VideoID != flagged {
		t.Errorf("match = %+v, want a video/spam match for %s", m, flagged)
	}
	if m.VideoTitle != "my mixtape" || m.AuthorUsername != "bob" {
		t.Errorf("match context = (%q, %q), want (my mixtape, bob)", m.VideoTitle, m.AuthorUsername)
	}
	if m.CommentID != "" || m.CommentBody != "" {
		t.Errorf("video match carries comment fields: %+v", m)
	}

	// An EDIT that newly introduces the term flags the clean video too.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+clean, `{"title":"now with Spam"}`, bob); rec.Code != http.StatusOK {
		t.Fatalf("edit = %d; body=%s", rec.Code, rec.Body.String())
	}
	got = matches()
	if len(got) != 2 {
		t.Fatalf("after edit, matches = %d, want 2", len(got))
	}
	if got[0].VideoID != clean || got[0].Type != "video" {
		t.Errorf("newest match = %+v, want the edited video", got[0])
	}

	// Re-editing with the same term stays idempotent (one row per term+video).
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+clean, `{"description":"still spam"}`, bob); rec.Code != http.StatusOK {
		t.Fatalf("re-edit = %d", rec.Code)
	}
	if got := matches(); len(got) != 2 {
		t.Errorf("after idempotent re-edit, matches = %d, want still 2", len(got))
	}
}
