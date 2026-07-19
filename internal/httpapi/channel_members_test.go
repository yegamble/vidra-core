package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// inviteEditor registers a user and has the channel owner invite them as an
// editor of `handle`, returning the editor's token and user id.
func inviteEditor(t *testing.T, srv *Server, ownerTok, handle, username, email string) (token, userID string) {
	t.Helper()
	token = registerAndToken(t, srv, `{"username":"`+username+`","email":"`+email+`","password":"supersecret"}`)
	rec := postJSONAuth(srv, "/api/v1/channels/"+handle+"/members", `{"handle":"`+username+`"}`, ownerTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite %s = %d; body=%s", username, rec.Code, rec.Body.String())
	}
	var m channelMemberView
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if m.Role != "editor" || m.Username != username {
		t.Fatalf("member = %+v", m)
	}
	return token, m.UserID
}

// TestChannelMemberEndpoints covers the members API surface: invite (201),
// unknown user (404), duplicate/owner (409), non-owner add/remove (403),
// roster visibility, and removal (204 + revoked authority).
func TestChannelMemberEndpoints(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	editorTok, editorID := inviteEditor(t, srv, ownerTok, "ada", "bob", "bob@example.test")
	strangerTok := registerAndToken(t, srv, `{"username":"carol","email":"carol@example.test","password":"supersecret"}`)

	// Unknown target user → 404.
	if rec := postJSONAuth(srv, "/api/v1/channels/ada/members", `{"handle":"ghost"}`, ownerTok); rec.Code != http.StatusNotFound {
		t.Errorf("unknown user invite = %d, want 404", rec.Code)
	}
	// Duplicate member → 409.
	if rec := postJSONAuth(srv, "/api/v1/channels/ada/members", `{"handle":"bob"}`, ownerTok); rec.Code != http.StatusConflict {
		t.Errorf("duplicate invite = %d, want 409", rec.Code)
	}
	// Inviting the owner → 409.
	if rec := postJSONAuth(srv, "/api/v1/channels/ada/members", `{"handle":"ada"}`, ownerTok); rec.Code != http.StatusConflict {
		t.Errorf("owner invite = %d, want 409", rec.Code)
	}
	// A non-owner (even an editor) cannot invite → 403.
	if rec := postJSONAuth(srv, "/api/v1/channels/ada/members", `{"handle":"carol"}`, editorTok); rec.Code != http.StatusForbidden {
		t.Errorf("editor invite = %d, want 403", rec.Code)
	}

	// Roster: owner and editor may view; a stranger gets 403.
	if rec := getWithAuth(srv, "/api/v1/channels/ada/members", ownerTok); rec.Code != http.StatusOK {
		t.Errorf("owner list members = %d, want 200", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/channels/ada/members", editorTok); rec.Code != http.StatusOK {
		t.Errorf("editor list members = %d, want 200", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/channels/ada/members", strangerTok); rec.Code != http.StatusForbidden {
		t.Errorf("stranger list members = %d, want 403", rec.Code)
	}

	// Non-owner cannot remove → 403.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/channels/ada/members/"+editorID, "", editorTok); rec.Code != http.StatusForbidden {
		t.Errorf("editor remove = %d, want 403", rec.Code)
	}
	// Owner removes the editor → 204; authority revoked.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/channels/ada/members/"+editorID, "", ownerTok); rec.Code != http.StatusNoContent {
		t.Fatalf("owner remove = %d, want 204", rec.Code)
	}
	if rec := postJSONAuth(srv, "/api/v1/channels/ada/videos", `{"title":"x","privacy":"private"}`, editorTok); rec.Code != http.StatusForbidden {
		t.Errorf("removed editor create video = %d, want 403", rec.Code)
	}

	// GET /me/channels tags roles: owner sees "owner"; a re-invited editor "editor".
	editorTok2, _ := inviteEditor(t, srv, ownerTok, "ada", "dave", "dave@example.test")
	ownerCh := getWithAuth(srv, "/api/v1/me/channels", ownerTok)
	var ownerList channelListResponse
	_ = json.Unmarshal(ownerCh.Body.Bytes(), &ownerList)
	if len(ownerList.Channels) != 1 || ownerList.Channels[0].Role != "owner" {
		t.Errorf("owner /me/channels roles = %+v", ownerList.Channels)
	}
	editorCh := getWithAuth(srv, "/api/v1/me/channels", editorTok2)
	var editorList channelListResponse
	_ = json.Unmarshal(editorCh.Body.Bytes(), &editorList)
	if len(editorList.Channels) != 1 || editorList.Channels[0].Role != "editor" || editorList.Channels[0].Handle != "ada" {
		t.Errorf("editor /me/channels = %+v", editorList.Channels)
	}
}

// TestEditorContentMatrix asserts the authorization matrix: the editor-ALLOWED
// content surfaces succeed for an editor collaborator, and the OWNER-ONLY
// surfaces reject them. A non-member stranger is blocked on the allowed set too.
func TestEditorContentMatrix(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	editorTok, editorID := inviteEditor(t, srv, ownerTok, "ada", "bob", "bob@example.test")
	strangerTok := registerAndToken(t, srv, `{"username":"carol","email":"carol@example.test","password":"supersecret"}`)

	// --- Editor CAN: content surfaces ---

	// Create a draft in the channel, then upload its source (editor uploads AS
	// the channel owner).
	draft := createVideo(t, srv, editorTok, "ada", `{"title":"draft","privacy":"private"}`)
	if rec := uploadVideoFile(srv, draft, "clip.mp4", "video/mp4", "tiny", editorTok); rec.Code != http.StatusCreated {
		t.Fatalf("editor upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	// Edit metadata.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+draft, `{"title":"edited"}`, editorTok); rec.Code != http.StatusOK {
		t.Errorf("editor edit = %d, want 200", rec.Code)
	}
	// Chapters.
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/videos/"+draft+"/chapters", `{"chapters":[{"start_seconds":0,"title":"Intro"}]}`, editorTok); rec.Code != http.StatusOK {
		t.Errorf("editor chapters = %d, want 200", rec.Code)
	}
	// Captions.
	if rec := uploadCaption(srv, draft, "en", "English", sampleVTT, editorTok, true); rec.Code != http.StatusCreated {
		t.Errorf("editor caption = %d, want 201", rec.Code)
	}
	// View channel stats + the full (drafts included) video list.
	if rec := getWithAuth(srv, "/api/v1/channels/ada/stats", editorTok); rec.Code != http.StatusOK {
		t.Errorf("editor stats = %d, want 200", rec.Code)
	}
	list := getWithAuth(srv, "/api/v1/channels/ada/videos", editorTok)
	if list.Code != http.StatusOK {
		t.Fatalf("editor list = %d, want 200", list.Code)
	}
	var feed struct {
		Videos []struct {
			ID string `json:"id"`
		} `json:"videos"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &feed)
	sawDraft := false
	for _, v := range feed.Videos {
		if v.ID == draft {
			sawDraft = true
		}
	}
	if !sawDraft {
		t.Errorf("editor channel video list did not include the private draft: %+v", feed.Videos)
	}
	// Manage live streams.
	if rec := postJSONAuth(srv, "/api/v1/channels/ada/live", `{"title":"stream","privacy":"public"}`, editorTok); rec.Code != http.StatusCreated {
		t.Errorf("editor live create = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	// Delete a channel video.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+draft, "", editorTok); rec.Code != http.StatusNoContent {
		t.Errorf("editor delete = %d, want 204", rec.Code)
	}

	// --- Editor CANNOT: owner-only surfaces ---

	ownerOnly := []struct {
		name, method, path, body string
		want                     int
	}{
		{"channel display patch", http.MethodPatch, "/api/v1/channels/ada", `{"display_name":"Hax"}`, http.StatusForbidden},
		{"protocol flags patch", http.MethodPatch, "/api/v1/channels/ada", `{"activitypub_enabled":false}`, http.StatusForbidden},
		{"channel delete", http.MethodDelete, "/api/v1/channels/ada", "", http.StatusForbidden},
		{"add member", http.MethodPost, "/api/v1/channels/ada/members", `{"handle":"carol"}`, http.StatusForbidden},
		{"remove member", http.MethodDelete, "/api/v1/channels/ada/members/" + editorID, "", http.StatusForbidden},
	}
	for _, tc := range ownerOnly {
		if rec := sendJSONAuth(srv, tc.method, tc.path, tc.body, editorTok); rec.Code != tc.want {
			t.Errorf("editor %s = %d, want %d; body=%s", tc.name, rec.Code, tc.want, rec.Body.String())
		}
	}

	// --- Stranger (no membership) is blocked on the editor-allowed set ---
	if rec := postJSONAuth(srv, "/api/v1/channels/ada/videos", `{"title":"x","privacy":"private"}`, strangerTok); rec.Code != http.StatusForbidden {
		t.Errorf("stranger create video = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/channels/ada/stats", strangerTok); rec.Code != http.StatusNotFound {
		t.Errorf("stranger stats = %d, want 404", rec.Code)
	}
	if rec := postJSONAuth(srv, "/api/v1/channels/ada/live", `{"title":"s","privacy":"public"}`, strangerTok); rec.Code != http.StatusForbidden {
		t.Errorf("stranger live create = %d, want 403", rec.Code)
	}
}
