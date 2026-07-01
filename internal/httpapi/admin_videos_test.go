package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// adminVideosBody parses GET /admin/videos.
type adminVideosBody struct {
	Videos []struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Privacy string `json:"privacy"`
		State   string `json:"state"`
		Blocked bool   `json:"blocked"`
	} `json:"videos"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func TestAdminVideosOverview(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	pub := createPublishedVideo(t, srv, admin, "ada", `{"title":"alpha video","privacy":"public"}`)
	draft := createVideo(t, srv, admin, "ada", `{"title":"beta draft","privacy":"private"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Block the published video.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+pub+"/block", `{"reason":"spam"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}

	parse := func(rec *httptest.ResponseRecorder) adminVideosBody {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("admin videos = %d; body=%s", rec.Code, rec.Body.String())
		}
		var body adminVideosBody
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body
	}

	// The admin sees ALL videos — including the private draft — with block status.
	body := parse(getWithAuth(srv, "/api/v1/admin/videos", admin))
	if len(body.Videos) != 2 {
		t.Fatalf("admin videos = %d, want 2 (public + private draft); body=%+v", len(body.Videos), body)
	}
	byID := map[string]struct {
		privacy, state string
		blocked        bool
	}{}
	for _, v := range body.Videos {
		byID[v.ID] = struct {
			privacy, state string
			blocked        bool
		}{v.Privacy, v.State, v.Blocked}
	}
	if p := byID[pub]; !p.blocked || p.privacy != "public" {
		t.Errorf("published video = %+v, want blocked/public", p)
	}
	if d := byID[draft]; d.blocked || d.privacy != "private" || d.state != "draft" {
		t.Errorf("draft video = %+v, want unblocked/private/draft", d)
	}

	// The q filter matches on title.
	if got := parse(getWithAuth(srv, "/api/v1/admin/videos?q=beta", admin)); len(got.Videos) != 1 || got.Videos[0].ID != draft {
		t.Errorf("q=beta = %+v, want only the draft", got.Videos)
	}

	// Regular users are forbidden; anonymous is unauthorized.
	if rec := getWithAuth(srv, "/api/v1/admin/videos", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-mod = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/videos", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon = %d, want 401", rec.Code)
	}
}
