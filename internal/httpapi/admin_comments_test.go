package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// adminCommentsBody parses GET /admin/comments.
type adminCommentsBody struct {
	Comments []struct {
		ID             string `json:"id"`
		VideoTitle     string `json:"video_title"`
		Body           string `json:"body"`
		AuthorUsername string `json:"author_username"`
	} `json:"comments"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func parseAdminComments(t *testing.T, rec *httptest.ResponseRecorder) adminCommentsBody {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("admin comments = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body adminCommentsBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body
}

func TestAdminCommentsOverview(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	charlie := registerAndToken(t, srv, `{"username":"charlie","email":"charlie@example.test","password":"supersecret"}`)

	for _, c := range []struct{ tok, body string }{{bob, "hello from bob"}, {charlie, "spam from charlie"}} {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/comments", `{"body":"`+c.body+`"}`, c.tok); rec.Code != http.StatusCreated {
			t.Fatalf("comment = %d; body=%s", rec.Code, rec.Body.String())
		}
	}

	// The admin sees all comments with author + video context.
	body := parseAdminComments(t, getWithAuth(srv, "/api/v1/admin/comments", admin))
	if len(body.Comments) != 2 {
		t.Fatalf("admin comments = %d, want 2; body=%+v", len(body.Comments), body)
	}
	for _, c := range body.Comments {
		if c.VideoTitle != "Clip" {
			t.Errorf("comment video_title = %q, want Clip", c.VideoTitle)
		}
	}

	// The q filter matches on body.
	if got := parseAdminComments(t, getWithAuth(srv, "/api/v1/admin/comments?q=spam", admin)); len(got.Comments) != 1 || got.Comments[0].AuthorUsername != "charlie" {
		t.Errorf("q=spam = %+v, want only charlie's", got.Comments)
	}

	// Regular users are forbidden; anonymous is unauthorized.
	if rec := getWithAuth(srv, "/api/v1/admin/comments", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-mod = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/comments", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon = %d, want 401", rec.Code)
	}
}

func TestModeratorDeletesAnyComment(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	crec := postJSONAuth(srv, "/api/v1/videos/"+vid+"/comments", `{"body":"bad comment"}`, bob)
	if crec.Code != http.StatusCreated {
		t.Fatalf("comment = %d; body=%s", crec.Code, crec.Body.String())
	}
	var cv commentView
	_ = json.Unmarshal(crec.Body.Bytes(), &cv)

	// A regular non-author still cannot delete it.
	other := registerAndToken(t, srv, `{"username":"eve","email":"eve@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/comments/"+cv.ID, "", other); rec.Code != http.StatusForbidden {
		t.Errorf("non-author non-mod delete = %d, want 403", rec.Code)
	}

	// The admin (moderator/admin) deletes bob's comment.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/comments/"+cv.ID, "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := parseAdminComments(t, getWithAuth(srv, "/api/v1/admin/comments", admin)); len(got.Comments) != 0 {
		t.Errorf("comments after admin delete = %d, want 0", len(got.Comments))
	}
}
