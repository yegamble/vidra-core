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

// TestEditorResumableUploadMatrix covers the chunked/resumable upload path for a
// collaborator (the single-shot multipart path is covered by
// TestEditorContentMatrix): an editor opens a session, streams the chunks, and
// completes it — and the stored bytes count against the channel OWNER, not the
// editor. A stranger cannot open a session for the channel's video.
func TestEditorResumableUploadMatrix(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	editorTok, _ := inviteEditor(t, srv, ownerTok, "ada", "bob", "bob@example.test")
	strangerTok := registerAndToken(t, srv, `{"username":"carol","email":"carol@example.test","password":"supersecret"}`)

	// Editor creates a draft in the channel, then drives the resumable protocol.
	draft := createVideo(t, srv, editorTok, "ada", `{"title":"chunked draft","privacy":"private"}`)

	// A stranger cannot open a session for the video (existence not leaked → 404).
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+draft+"/upload-session",
		`{"size":12,"filename":"clip.mp4"}`, strangerTok); rec.Code != http.StatusNotFound {
		t.Errorf("stranger open session = %d, want 404", rec.Code)
	}

	payload := []byte("editor-bytes") // 12 bytes → a single 12-byte chunk
	sess := openUploadSession(t, srv, draft, "clip.mp4", len(payload), editorTok)
	if rec := putChunkAuth(srv, sess.UploadID, 0, payload, editorTok); rec.Code != http.StatusOK {
		t.Fatalf("editor put chunk = %d; body=%s", rec.Code, rec.Body.String())
	}
	// A stranger cannot see the editor's session either.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/uploads/"+sess.UploadID, "", strangerTok); rec.Code != http.StatusNotFound {
		t.Errorf("stranger session status = %d, want 404", rec.Code)
	}
	compRec := sendJSONAuth(srv, http.MethodPost, "/api/v1/uploads/"+sess.UploadID+"/complete", "", editorTok)
	if compRec.Code != http.StatusCreated {
		t.Fatalf("editor complete = %d; body=%s", compRec.Code, compRec.Body.String())
	}

	// Owner-attribution: the assembled bytes land against the channel OWNER's
	// quota, and the editor's own usage stays zero.
	if st := getMyQuota(t, srv, ownerTok); st.UsedBytes != int64(len(payload)) {
		t.Errorf("owner used_bytes = %d, want %d (upload attributed to the owner)", st.UsedBytes, len(payload))
	}
	if st := getMyQuota(t, srv, editorTok); st.UsedBytes != 0 {
		t.Errorf("editor used_bytes = %d, want 0 (upload must not count against the editor)", st.UsedBytes)
	}
}

// TestEditorLiveManagementMatrix covers the per-stream live surfaces beyond
// create (which TestEditorContentMatrix already asserts): an editor may update,
// regenerate the key, read a private stream's details, and delete a channel's
// live stream. A stranger is refused on each — 404 on the id-scoped routes
// (existence not leaked) and 403 on the channel-scoped list.
func TestEditorLiveManagementMatrix(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	editorTok, _ := inviteEditor(t, srv, ownerTok, "ada", "bob", "bob@example.test")
	strangerTok := registerAndToken(t, srv, `{"username":"carol","email":"carol@example.test","password":"supersecret"}`)

	// Owner creates a PRIVATE stream so the get-private-details check is meaningful.
	createRec := postJSONAuth(srv, "/api/v1/channels/ada/live", `{"title":"stream","privacy":"private"}`, ownerTok)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("owner live create = %d; body=%s", createRec.Code, createRec.Body.String())
	}
	var created createLiveStreamResponse
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)
	streamID := created.LiveStream.ID

	// --- Editor CAN manage the stream ---
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/live/"+streamID, `{"title":"edited","privacy":"private"}`, editorTok); rec.Code != http.StatusOK {
		t.Errorf("editor live update = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/live/"+streamID+"/key", "", editorTok); rec.Code != http.StatusOK {
		t.Errorf("editor regenerate key = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Editor reads the private stream's details (a stranger would 404 below).
	if rec := getWithAuth(srv, "/api/v1/live/"+streamID, editorTok); rec.Code != http.StatusOK {
		t.Errorf("editor get private stream = %d, want 200", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/channels/ada/live", editorTok); rec.Code != http.StatusOK {
		t.Errorf("editor list streams = %d, want 200", rec.Code)
	}

	// --- Stranger is refused everywhere ---
	if rec := getWithAuth(srv, "/api/v1/live/"+streamID, strangerTok); rec.Code != http.StatusNotFound {
		t.Errorf("stranger get private stream = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/live/"+streamID, `{"title":"hax","privacy":"private"}`, strangerTok); rec.Code != http.StatusNotFound {
		t.Errorf("stranger live update = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/live/"+streamID+"/key", "", strangerTok); rec.Code != http.StatusNotFound {
		t.Errorf("stranger regenerate key = %d, want 404", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/channels/ada/live", strangerTok); rec.Code != http.StatusForbidden {
		t.Errorf("stranger list streams = %d, want 403", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/live/"+streamID, "", strangerTok); rec.Code != http.StatusNotFound {
		t.Errorf("stranger live delete = %d, want 404", rec.Code)
	}

	// --- Editor deletes the stream (done last so the checks above still had a target) ---
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/live/"+streamID, "", editorTok); rec.Code != http.StatusNoContent {
		t.Errorf("editor live delete = %d, want 204", rec.Code)
	}
}

// TestEditorImportReplaceThumbnailCaptionMatrix covers the remaining
// editor-allowed content surfaces with no prior collaborator coverage: URL
// import, source replace, thumbnail set (image) + select-frame (JSON), and
// caption delete. A stranger is refused (404, existence not leaked) on each.
// It uses frameServer so a processed original exists for the frame-pick variant,
// and enables the (default-off) video_replace feature.
func TestEditorImportReplaceThumbnailCaptionMatrix(t *testing.T) {
	frame := []byte("\xff\xd8\xff\xe0framejpeg")
	srv := frameServer(t, frame)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada") // first user → admin
	editorTok, _ := inviteEditor(t, srv, ownerTok, "ada", "bob", "bob@example.test")
	strangerTok := registerAndToken(t, srv, `{"username":"carol","email":"carol@example.test","password":"supersecret"}`)
	patchSettings(t, srv, ownerTok, `{"video_replace_enabled":true}`) // owner is admin here

	// --- URL import (editor-allowed; enqueue is 202) ---
	importVideo := createVideo(t, srv, editorTok, "ada", `{"title":"to import","privacy":"public"}`)
	enqueueImport(t, srv, importVideo, "https://example.com/clip.mp4", editorTok)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+importVideo+"/import",
		`{"url":"https://example.com/clip.mp4"}`, strangerTok); rec.Code != http.StatusNotFound {
		t.Errorf("stranger import = %d, want 404", rec.Code)
	}

	// --- Source replace (editor-allowed; a published video, feature enabled) ---
	replaceVideo := publishVideoWithFile(t, srv, ownerTok, "ada", "Clip", "v0 bytes")
	if rec := replaceVideoFileReq(srv, replaceVideo, "v1.mp4", "video/mp4", "replacement bytes", editorTok); rec.Code != http.StatusOK {
		t.Errorf("editor replace = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec := replaceVideoFileReq(srv, replaceVideo, "v2.mp4", "video/mp4", "hax bytes", strangerTok); rec.Code != http.StatusNotFound {
		t.Errorf("stranger replace = %d, want 404", rec.Code)
	}

	// --- Thumbnail: set (image) and select-frame (JSON) ---
	thumbVideo := publishVideoWithFile(t, srv, ownerTok, "ada", "Poster", "orig bytes")
	if rec := uploadThumbnail(srv, thumbVideo, "poster.png", "image/png", "\x89PNG\r\n-fake", editorTok); rec.Code != http.StatusCreated {
		t.Errorf("editor set thumbnail = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if rec := frameThumbnail(srv, thumbVideo, `{"at_seconds":5}`, editorTok); rec.Code != http.StatusCreated {
		t.Errorf("editor select-frame thumbnail = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if rec := uploadThumbnail(srv, thumbVideo, "poster.png", "image/png", "img", strangerTok); rec.Code != http.StatusNotFound {
		t.Errorf("stranger set thumbnail = %d, want 404", rec.Code)
	}

	// --- Caption delete (editor-allowed) ---
	captionVideo := createVideo(t, srv, editorTok, "ada", `{"title":"captioned","privacy":"private"}`)
	if rec := uploadCaption(srv, captionVideo, "en", "English", sampleVTT, editorTok, true); rec.Code != http.StatusCreated {
		t.Fatalf("seed caption = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+captionVideo+"/captions/en", "", strangerTok); rec.Code != http.StatusNotFound {
		t.Errorf("stranger caption delete = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+captionVideo+"/captions/en", "", editorTok); rec.Code != http.StatusNoContent {
		t.Errorf("editor caption delete = %d, want 204", rec.Code)
	}
}
