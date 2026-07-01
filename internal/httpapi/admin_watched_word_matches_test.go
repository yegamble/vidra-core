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
}
