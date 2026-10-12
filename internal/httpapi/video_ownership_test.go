package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func giveOwnership(srv *Server, tok, videoID, username string) *httptest.ResponseRecorder {
	return postJSONAuth(srv, "/api/v1/videos/"+videoID+"/give-ownership", `{"username":"`+username+`"}`, tok)
}

// ownershipNotices counts the caller's video_ownership_request notifications
// naming actor and videoTitle.
func ownershipNotices(t *testing.T, srv *Server, tok, actor, videoTitle string) int {
	t.Helper()
	rec := getWithAuth(srv, "/api/v1/me/notifications", tok)
	var body struct {
		Notifications []struct {
			Type       string `json:"type"`
			VideoTitle string `json:"video_title"`
			Actor      struct {
				Username string `json:"username"`
			} `json:"actor"`
		} `json:"notifications"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("notifications = %d; body=%s", rec.Code, rec.Body.String())
	}
	n := 0
	for _, it := range body.Notifications {
		if it.Type == "video_ownership_request" && it.Actor.Username == actor && it.VideoTitle == videoTitle {
			n++
		}
	}
	return n
}

// TestGiveVideoOwnership is the give-side matrix (T21). Only the video's OWNER
// may offer it — not an editor collaborator, not staff — to an existing, active,
// unblocked local account other than themselves; one pending request per video;
// only once the upload has settled. The recipient is notified.
func TestGiveVideoOwnership(t *testing.T) {
	srv, repo, own := ownershipServer(t)
	adminTok := createChannelFor(t, srv, "root", "root@example.test", "root") // first user = admin
	adaTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	editorTok, _ := inviteEditor(t, srv, adaTok, "ada", "eve", "eve@example.test")
	carolTok := registerAndToken(t, srv, `{"username":"carol","email":"carol@example.test","password":"supersecret"}`)
	vid := createPublishedVideo(t, srv, adaTok, "ada", `{"title":"Gift Clip","privacy":"private"}`)

	for _, c := range []struct{ name, tok string }{{"stranger", bobTok}, {"editor", editorTok}, {"admin", adminTok}} {
		if rec := giveOwnership(srv, c.tok, vid, "carol"); rec.Code != http.StatusNotFound {
			t.Errorf("%s give = %d, want 404; body=%s", c.name, rec.Code, rec.Body.String())
		}
	}
	if rec := giveOwnership(srv, adaTok, vid, "ADA"); rec.Code != http.StatusBadRequest {
		t.Errorf("give to self = %d, want 400", rec.Code)
	}
	if rec := giveOwnership(srv, adaTok, vid, " "); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("blank username = %d, want 422", rec.Code)
	}

	// Unknown, banned and blocked recipients get one indistinguishable answer.
	errMsg := func(rec *httptest.ResponseRecorder) string {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		return e.Error.Message
	}
	unknown := giveOwnership(srv, adaTok, vid, "nobody")
	if unknown.Code != http.StatusNotFound || errMsg(unknown) == "" {
		t.Fatalf("unknown recipient = %d %s, want 404", unknown.Code, unknown.Body.String())
	}
	carol := repo.users.users["carol@example.test"]
	carol.IsActive = false
	repo.users.users["carol@example.test"] = carol
	if rec := giveOwnership(srv, adaTok, vid, "carol"); rec.Code != unknown.Code || errMsg(rec) != errMsg(unknown) {
		t.Errorf("banned recipient = %d %s, want the unknown answer", rec.Code, rec.Body.String())
	}
	carol.IsActive = true
	repo.users.users["carol@example.test"] = carol
	adaID := repo.users.users["ada@example.test"].ID.String()
	if rec := postJSONAuth(srv, "/api/v1/me/blocks/"+adaID, "", carolTok); rec.Code >= 300 {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := giveOwnership(srv, adaTok, vid, "carol"); rec.Code != unknown.Code || errMsg(rec) != errMsg(unknown) {
		t.Errorf("recipient who blocked the giver = %d %s, want the unknown answer", rec.Code, rec.Body.String())
	}

	// A video still uploading cannot be given: the accept-time quota check
	// would weigh a file that is not there yet.
	draft := createVideo(t, srv, adaTok, "ada", `{"title":"Draft"}`)
	if rec := giveOwnership(srv, adaTok, draft, "bob"); rec.Code != http.StatusConflict {
		t.Errorf("draft give = %d, want 409", rec.Code)
	}
	if len(own.rows) != 0 {
		t.Fatalf("refused gives created requests: %+v", own.rows)
	}

	if rec := giveOwnership(srv, adaTok, vid, "Bob"); rec.Code != http.StatusNoContent {
		t.Fatalf("give = %d; body=%s", rec.Code, rec.Body.String())
	}
	if n := ownershipNotices(t, srv, bobTok, "ada", "Gift Clip"); n != 1 {
		t.Errorf("bob's ownership notifications = %d, want 1", n)
	}
	if rec := giveOwnership(srv, adaTok, vid, "root"); rec.Code != http.StatusConflict {
		t.Errorf("second pending give = %d, want 409", rec.Code)
	}
	if len(own.rows) != 1 || repo.videos[own.rows[0].VideoID].Title != "Gift Clip" {
		t.Errorf("requests = %+v, want exactly one for the gift", own.rows)
	}
}
