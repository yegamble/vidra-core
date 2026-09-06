package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// uploadAttachment posts a multipart "file" to the DM-attachment endpoint.
func uploadAttachment(srv *Server, convID, filename, contentType, content, token string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{`form-data; name="file"; filename="` + filename + `"`}
	if contentType != "" {
		hdr["Content-Type"] = []string{contentType}
	}
	part, _ := w.CreatePart(hdr)
	_, _ = part.Write([]byte(content))
	_ = w.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations/"+convID+"/attachments", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// startDMConversation registers two users, opens a conversation, returns tokens,
// ids, and the conversation id.
func startDMConversation(t *testing.T, srv *Server) (adaTok, adaID, bobTok, bobID, convID string) {
	t.Helper()
	adaTok, adaID = registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bobTok, bobID = registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+bobID+`"}`, adaTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start conversation = %d; body=%s", rec.Code, rec.Body.String())
	}
	var conv conversationView
	_ = json.Unmarshal(rec.Body.Bytes(), &conv)
	return adaTok, adaID, bobTok, bobID, conv.ID
}

func TestDMAttachmentFlow(t *testing.T) {
	srv := videoServer(t)
	adaTok, _, bobTok, _, convID := startDMConversation(t, srv)

	// Unsupported type → 415.
	if rec := uploadAttachment(srv, convID, "a.exe", "application/octet-stream", "x", adaTok); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("exe upload = %d, want 415; body=%s", rec.Code, rec.Body.String())
	}
	// Valid image upload.
	rec := uploadAttachment(srv, convID, "pic.png", "image/png", "\x89PNGfake", adaTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var up uploadAttachmentResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &up)
	if up.AttachmentID == "" || up.Kind != "image" {
		t.Fatalf("upload resp = %+v", up)
	}
	// Send referencing it.
	send := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+convID+"/messages",
		`{"body":"look","attachment_ids":["`+up.AttachmentID+`"]}`, adaTok)
	if send.Code != http.StatusCreated {
		t.Fatalf("send = %d, want 201; body=%s", send.Code, send.Body.String())
	}
	var msg messageView
	_ = json.Unmarshal(send.Body.Bytes(), &msg)
	if len(msg.Attachments) != 1 {
		t.Fatalf("send attachments = %+v", msg.Attachments)
	}
	// Bob (participant) can download; a stranger cannot.
	dl := sendJSONAuth(srv, http.MethodGet, "/api/v1/attachments/"+up.AttachmentID, "", bobTok)
	if dl.Code != http.StatusOK || dl.Body.String() != "\x89PNGfake" {
		t.Fatalf("bob download = %d body=%q", dl.Code, dl.Body.String())
	}
	strangerTok, _ := registerAndUser(t, srv, `{"username":"eve","email":"eve@example.test","password":"supersecret"}`)
	if s := sendJSONAuth(srv, http.MethodGet, "/api/v1/attachments/"+up.AttachmentID, "", strangerTok); s.Code != http.StatusNotFound {
		t.Fatalf("stranger download = %d, want 404", s.Code)
	}
	// A non-participant cannot upload to the conversation → 404.
	if s := uploadAttachment(srv, convID, "x.png", "image/png", "x", strangerTok); s.Code != http.StatusNotFound {
		t.Fatalf("stranger upload = %d, want 404", s.Code)
	}
}

func TestDMReadReceipts(t *testing.T) {
	srv := videoServer(t)
	adaTok, _, bobTok, _, convID := startDMConversation(t, srv)

	// Bob sends two messages.
	for _, b := range []string{"one", "two"} {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+convID+"/messages", `{"body":"`+b+`"}`, bobTok); rec.Code != http.StatusCreated {
			t.Fatalf("bob send = %d", rec.Code)
		}
	}
	// Ada's inbox shows 2 unread.
	inbox := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/conversations", "", adaTok)
	var list conversationListResponse
	_ = json.Unmarshal(inbox.Body.Bytes(), &list)
	if len(list.Conversations) != 1 || list.Conversations[0].UnreadCount != 2 {
		t.Fatalf("ada inbox unread = %+v, want 2", list.Conversations)
	}
	// Ada marks read.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+convID+"/read", ``, adaTok); rec.Code != http.StatusNoContent {
		t.Fatalf("mark read = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	inbox = sendJSONAuth(srv, http.MethodGet, "/api/v1/me/conversations", "", adaTok)
	_ = json.Unmarshal(inbox.Body.Bytes(), &list)
	if list.Conversations[0].UnreadCount != 0 {
		t.Errorf("after read unread = %d, want 0", list.Conversations[0].UnreadCount)
	}
	// Bob's thread view exposes Ada's read watermark.
	thread := sendJSONAuth(srv, http.MethodGet, "/api/v1/conversations/"+convID+"/messages", "", bobTok)
	var mlr messageListResponse
	_ = json.Unmarshal(thread.Body.Bytes(), &mlr)
	if mlr.PeerLastReadMessageID == "" {
		t.Error("peer_last_read_message_id missing after Ada read")
	}
	// Ada disables read receipts → Bob no longer sees her watermark.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/me/messaging-prefs", `{"read_receipts":false}`, adaTok); rec.Code != http.StatusOK {
		t.Fatalf("patch prefs = %d; body=%s", rec.Code, rec.Body.String())
	}
	thread = sendJSONAuth(srv, http.MethodGet, "/api/v1/conversations/"+convID+"/messages", "", bobTok)
	var mlr2 messageListResponse
	_ = json.Unmarshal(thread.Body.Bytes(), &mlr2)
	if mlr2.PeerLastReadMessageID != "" {
		t.Error("peer watermark visible after Ada disabled read receipts")
	}
}

func TestDMMessagingPrefsDefault(t *testing.T) {
	srv := videoServer(t)
	tok, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/messaging-prefs", "", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("get prefs = %d", rec.Code)
	}
	var v messagingPrefsView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if !v.ReadReceipts {
		t.Error("read_receipts default = false, want true")
	}
}

func TestDMDeleteMessage(t *testing.T) {
	srv := videoServer(t)
	adaTok, _, bobTok, _, convID := startDMConversation(t, srv)

	send := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+convID+"/messages", `{"body":"secret"}`, adaTok)
	var msg messageView
	_ = json.Unmarshal(send.Body.Bytes(), &msg)

	// Bob (not the sender) cannot delete → 404.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/messages/"+msg.ID, "", bobTok); rec.Code != http.StatusNotFound {
		t.Fatalf("bob delete = %d, want 404", rec.Code)
	}
	// Ada deletes → 204, tombstoned.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/messages/"+msg.ID, "", adaTok); rec.Code != http.StatusNoContent {
		t.Fatalf("ada delete = %d, want 204", rec.Code)
	}
	thread := sendJSONAuth(srv, http.MethodGet, "/api/v1/conversations/"+convID+"/messages", "", bobTok)
	var mlr messageListResponse
	_ = json.Unmarshal(thread.Body.Bytes(), &mlr)
	if len(mlr.Messages) != 1 || !mlr.Messages[0].Deleted || mlr.Messages[0].Body != "[deleted]" {
		t.Fatalf("after delete = %+v", mlr.Messages)
	}
}

func TestDMReportMessage(t *testing.T) {
	srv := videoServer(t)
	adaTok, _, bobTok, _, convID := startDMConversation(t, srv)
	send := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+convID+"/messages", `{"body":"rude"}`, adaTok)
	var msg messageView
	_ = json.Unmarshal(send.Body.Bytes(), &msg)

	// Bob (participant) reports it.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/messages/"+msg.ID+"/report", `{"reason":"harassment"}`, bobTok); rec.Code != http.StatusNoContent {
		t.Fatalf("report = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	// A stranger cannot report → 404.
	strangerTok, _ := registerAndUser(t, srv, `{"username":"eve","email":"eve@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/messages/"+msg.ID+"/report", `{"reason":"x"}`, strangerTok); rec.Code != http.StatusNotFound {
		t.Fatalf("stranger report = %d, want 404", rec.Code)
	}

	// The report has to be ACTIONABLE, not merely accepted. A conversation is
	// private, so the body snapshot taken at report time is the only thing a
	// moderator will ever see of the reported message — the queue is useless
	// without it. Asserting only the 204 above is what let the HTTP projection
	// drop both fields while the OpenAPI contract kept promising them.
	var queue reportListResponse
	_ = json.Unmarshal(listReports(srv, "", adaTok).Body.Bytes(), &queue)
	var got *reportView
	for i := range queue.Reports {
		if queue.Reports[i].TargetType == "message" {
			got = &queue.Reports[i]
		}
	}
	if got == nil {
		t.Fatalf("no message report in the queue: %+v", queue.Reports)
	}
	if got.MessageID != msg.ID {
		t.Errorf("message_id = %q, want %q", got.MessageID, msg.ID)
	}
	if got.MessageBody != "rude" {
		t.Errorf("message_body = %q, want %q — the moderator cannot read the reported message", got.MessageBody, "rude")
	}
}
