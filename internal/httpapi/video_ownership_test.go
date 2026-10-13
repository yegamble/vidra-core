package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func giveOwnership(srv *Server, tok, videoID, username string) *httptest.ResponseRecorder {
	return postJSONAuth(srv, "/api/v1/videos/"+videoID+"/give-ownership", `{"username":"`+username+`"}`, tok)
}

// ownershipNotices counts the caller's video_ownership_request notifications
// naming actor. They carry no video (see NotifyVideoOwnershipRequest).
func ownershipNotices(t *testing.T, srv *Server, tok, actor string) int {
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
		if it.Type == "video_ownership_request" && it.Actor.Username == actor {
			if it.VideoTitle != "" {
				t.Errorf("an ownership notification carries a video title: %q", it.VideoTitle)
			}
			n++
		}
	}
	return n
}

// TestGiveVideoOwnership is the give-side matrix (T21). Only the video's OWNER
// may offer it — not an editor collaborator, not staff — to an existing, active,
// unblocked local account other than themselves; one pending request per video;
// only once the upload has settled. Every ineligible recipient, the giver
// included, gets one answer. The recipient is notified.
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
	if rec := giveOwnership(srv, adaTok, vid, "ADA"); rec.Code != unknown.Code || errMsg(rec) != errMsg(unknown) {
		t.Errorf("give to self = %d %s, want the unknown answer", rec.Code, rec.Body.String())
	}
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
	if n := ownershipNotices(t, srv, bobTok, "ada"); n != 1 {
		t.Errorf("bob's ownership notifications = %d, want 1", n)
	}
	// A pending request is answered BEFORE the name is looked at, so a second
	// give cannot tell an eligible account from an unknown one.
	for _, name := range []string{"root", "nobody", "carol"} {
		if rec := giveOwnership(srv, adaTok, vid, name); rec.Code != http.StatusConflict {
			t.Errorf("second pending give to %s = %d, want 409", name, rec.Code)
		}
	}
	if len(own.rows) != 1 || repo.videos[own.rows[0].VideoID].Title != "Gift Clip" {
		t.Errorf("requests = %+v, want exactly one for the gift", own.rows)
	}
}

// TestCancelVideoOwnership: the giver withdraws a pending offer; nobody else
// can, and the video can be offered again — to the same person only after the
// re-offer cooldown.
func TestCancelVideoOwnership(t *testing.T) {
	srv, _, own := ownershipServer(t)
	adaTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	carolTok := createChannelFor(t, srv, "carol", "carol@example.test", "carol")
	vid := createPublishedVideo(t, srv, adaTok, "ada", `{"title":"Gift"}`)
	cancelGive := func(tok, video string) int {
		return sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+video+"/give-ownership", "", tok).Code
	}
	if code := cancelGive(adaTok, vid); code != http.StatusNotFound {
		t.Errorf("cancel with nothing pending = %d, want 404", code)
	}
	if rec := giveOwnership(srv, adaTok, vid, "bob"); rec.Code != http.StatusNoContent {
		t.Fatalf("give = %d; body=%s", rec.Code, rec.Body.String())
	}
	for _, tok := range []string{bobTok, carolTok} {
		if code := cancelGive(tok, vid); code != http.StatusNotFound {
			t.Errorf("cancel by a non-giver = %d, want 404", code)
		}
	}
	if code := cancelGive(adaTok, uuid.NewString()); code != http.StatusNotFound {
		t.Errorf("cancel for an unknown video = %d, want 404", code)
	}
	if code := cancelGive(adaTok, vid); code != http.StatusNoContent || own.rows[0].Status != "cancelled" {
		t.Fatalf("giver cancel = %d, status %q", code, own.rows[0].Status)
	}
	if code := cancelGive(adaTok, vid); code != http.StatusNotFound {
		t.Errorf("second cancel = %d, want 404", code)
	}
	// Withdrawing starts the same cooldown as a refusal: offer → withdraw →
	// offer again to the same person is refused and pushes no new notice.
	if rec := giveOwnership(srv, adaTok, vid, "bob"); rec.Code != http.StatusConflict {
		t.Errorf("re-offer to the same person right after withdrawing = %d, want 409", rec.Code)
	}
	if n := ownershipNotices(t, srv, bobTok, "ada"); n != 1 {
		t.Errorf("bob's ownership notifications = %d, want 1", n)
	}
	if rec := giveOwnership(srv, adaTok, vid, "carol"); rec.Code != http.StatusNoContent {
		t.Errorf("offer to someone else after withdrawing = %d, want 204", rec.Code)
	}
}

// TestStaleOwnershipRequestDoesNotBlock: a pending request whose giver no
// longer owns the video, or that outlived the offer window, is expired by the
// next give instead of blocking the video's current owner.
func TestStaleOwnershipRequestDoesNotBlock(t *testing.T) {
	srv, repo, own := ownershipServer(t)
	adaTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	carolTok := createChannelFor(t, srv, "carol", "carol@example.test", "carol")
	createChannelFor(t, srv, "dave", "dave@example.test", "dave")
	vid := createPublishedVideo(t, srv, adaTok, "ada", `{"title":"Gift"}`)
	if rec := giveOwnership(srv, adaTok, vid, "bob"); rec.Code != http.StatusNoContent {
		t.Fatalf("give = %d; body=%s", rec.Code, rec.Body.String())
	}
	// ada loses the video (it now lives in carol's channel).
	row := repo.videos[uuid.MustParse(vid)]
	row.ChannelID, row.OwnerID = uuid.MustParse(channelIDOf(t, srv, "carol")), repo.users.users["carol@example.test"].ID
	repo.videos[uuid.MustParse(vid)] = row
	if rec := giveOwnership(srv, carolTok, vid, "bob"); rec.Code != http.StatusNoContent {
		t.Fatalf("new owner give over a stale request = %d; body=%s", rec.Code, rec.Body.String())
	}
	if own.rows[0].Status != "expired" || own.rows[1].Status != "pending" {
		t.Errorf("statuses = %q, %q; want expired, pending", own.rows[0].Status, own.rows[1].Status)
	}
	// carol's request outlives the offer window: the next give expires it.
	own.rows[1].CreatedAt = time.Now().Add(-31 * 24 * time.Hour)
	if rec := giveOwnership(srv, carolTok, vid, "dave"); rec.Code != http.StatusNoContent {
		t.Fatalf("give over an expired request = %d; body=%s", rec.Code, rec.Body.String())
	}
	if own.rows[1].Status != "expired" {
		t.Errorf("old request status = %q, want expired", own.rows[1].Status)
	}
}

// TestOwnershipRefusalCooldown: after a refusal the same giver cannot offer
// the same recipient another video for a day; other recipients are unaffected.
func TestOwnershipRefusalCooldown(t *testing.T) {
	srv, _, own := ownershipServer(t)
	adaTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	createChannelFor(t, srv, "carol", "carol@example.test", "carol")
	first := createPublishedVideo(t, srv, adaTok, "ada", `{"title":"One"}`)
	second := createPublishedVideo(t, srv, adaTok, "ada", `{"title":"Two"}`)
	if rec := giveOwnership(srv, adaTok, first, "bob"); rec.Code != http.StatusNoContent {
		t.Fatalf("give = %d", rec.Code)
	}
	own.rows[0].Status, own.rows[0].UpdatedAt = "refused", time.Now() // bob said no
	if rec := giveOwnership(srv, adaTok, second, "bob"); rec.Code != http.StatusConflict {
		t.Errorf("re-offer inside the cooldown = %d, want 409", rec.Code)
	}
	if rec := giveOwnership(srv, adaTok, second, "carol"); rec.Code != http.StatusNoContent {
		t.Errorf("offer to someone else = %d, want 204", rec.Code)
	}
	own.rows[1].Status = "cancelled"
	own.rows[0].UpdatedAt = time.Now().Add(-25 * time.Hour)
	if rec := giveOwnership(srv, adaTok, second, "bob"); rec.Code != http.StatusNoContent {
		t.Errorf("re-offer after the cooldown = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}
