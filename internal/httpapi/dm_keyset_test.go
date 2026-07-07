package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// sendDM posts a plaintext message and returns its id.
func sendDM(t *testing.T, srv *Server, convID, body, token string) string {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+convID+"/messages", `{"body":"`+body+`"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send %q = %d; body=%s", body, rec.Code, rec.Body.String())
	}
	var mv messageView
	_ = json.Unmarshal(rec.Body.Bytes(), &mv)
	return mv.ID
}

func listMessages(t *testing.T, srv *Server, convID, query, token string) messageListResponse {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/conversations/"+convID+"/messages"+query, "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("list%s = %d; body=%s", query, rec.Code, rec.Body.String())
	}
	var mlr messageListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &mlr)
	return mlr
}

func bodies(mlr messageListResponse) []string {
	out := make([]string, len(mlr.Messages))
	for i, m := range mlr.Messages {
		out[i] = m.Body
	}
	return out
}

// TestDMKeysetPagination drives before_id keyset paging over HTTP: stable
// newest-first pages, an empty boundary page, and the 422/404 error contract.
func TestDMKeysetPagination(t *testing.T) {
	srv := videoServer(t)
	adaTok, _, _, _, convID := startDMConversation(t, srv)

	m1 := sendDM(t, srv, convID, "m1", adaTok)
	m2 := sendDM(t, srv, convID, "m2", adaTok)
	_ = sendDM(t, srv, convID, "m3", adaTok)

	// Page 1: newest two.
	p1 := listMessages(t, srv, convID, "?limit=2", adaTok)
	if got := bodies(p1); len(got) != 2 || got[0] != "m3" || got[1] != "m2" {
		t.Fatalf("page1 = %v, want [m3 m2]", got)
	}

	// Keyset page 2 from the oldest of page 1 (m2): only older messages.
	p2 := listMessages(t, srv, convID, "?limit=2&before_id="+m2, adaTok)
	if got := bodies(p2); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("before m2 = %v, want [m1]", got)
	}

	// Exact boundary: before the oldest message is an empty page.
	if got := bodies(listMessages(t, srv, convID, "?before_id="+m1, adaTok)); len(got) != 0 {
		t.Fatalf("before m1 = %v, want empty", got)
	}

	// before_id + offset → 422 (mutually exclusive).
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/conversations/"+convID+"/messages?before_id="+m2+"&offset=1", "", adaTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("both params = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	// Malformed before_id → 422.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/conversations/"+convID+"/messages?before_id=not-a-uuid", "", adaTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed before_id = %d, want 422", rec.Code)
	}
	// Unknown before_id → 422.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/conversations/"+convID+"/messages?before_id="+uuid.NewString(), "", adaTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown before_id = %d, want 422", rec.Code)
	}

	// A cursor from a DIFFERENT conversation → 422 (create a genuinely foreign
	// conversation with a third user).
	carolTok, _ := registerAndUser(t, srv, `{"username":"carol","email":"carol@example.test","password":"supersecret"}`)
	foreignConv := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_username":"carol"}`, adaTok)
	var fc conversationView
	_ = json.Unmarshal(foreignConv.Body.Bytes(), &fc)
	foreignMsg := sendDM(t, srv, fc.ID, "elsewhere", adaTok)
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/conversations/"+convID+"/messages?before_id="+foreignMsg, "", adaTok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("foreign-conversation cursor = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}

	// A non-participant is still 404 (existence not leaked), even with a cursor.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/conversations/"+convID+"/messages?before_id="+m2, "", carolTok); rec.Code != http.StatusNotFound {
		t.Fatalf("non-participant keyset = %d, want 404", rec.Code)
	}
}
