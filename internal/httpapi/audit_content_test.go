package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/observability"
)

// Destructive content deletions must leave an audit trail (P15 / observability.md
// line 83). The audit() helper reads s.logger at call time, so pointing it at a
// buffer after construction captures the emitted events.

func TestDeleteVideoEmitsAudit(t *testing.T) {
	srv := videoServer(t)
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))

	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, ownerTok, "ada", `{"title":"t","privacy":"public"}`)
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+id, "", ownerTok); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", rec.Code)
	}

	ev := findAudit(auditEvents(t, &buf), observability.ActionVideoDelete, observability.ResultSuccess)
	if ev == nil {
		t.Fatalf("no %s audit event emitted on video delete", observability.ActionVideoDelete)
	}
	if ev["actor_id"] == "" || ev["actor_id"] == nil {
		t.Errorf("video delete audit event missing actor_id: %v", ev)
	}
}

// TestVideoManagementPolicy proves that moderators/admins can PATCH and DELETE
// another user's local video, ordinary non-owners remain hidden behind 404,
// and a managed deletion is audited as the moderator who acted (not the owner).
func TestVideoManagementPolicy(t *testing.T) {
	srv, _, _, _, repo := videoServerFull(t, testConfig())
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))

	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	ownerTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	regularTok := registerAndToken(t, srv, `{"username":"eve","email":"eve@example.test","password":"supersecret"}`)
	_, moderatorID := registerAndUser(t, srv, `{"username":"mia","email":"mia@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+moderatorID,
		`{"role":"moderator"}`, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("promote moderator = %d; body=%s", rec.Code, rec.Body.String())
	}
	// Role is carried in the access token, so log in again after promotion.
	login := postTo(srv, "/api/v1/auth/login", `{"email":"mia@example.test","password":"supersecret"}`)
	if login.Code != http.StatusOK {
		t.Fatalf("moderator login = %d; body=%s", login.Code, login.Body.String())
	}
	var moderatorAuth authResponse
	if err := json.Unmarshal(login.Body.Bytes(), &moderatorAuth); err != nil {
		t.Fatalf("unmarshal moderator login: %v", err)
	}
	if moderatorAuth.User.Role != "moderator" {
		t.Fatalf("fresh moderator role = %q, want moderator", moderatorAuth.User.Role)
	}
	moderatorTok := moderatorAuth.Token

	adminUpdateID := createVideo(t, srv, ownerTok, "bob", `{"title":"admin old","privacy":"private"}`)
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+adminUpdateID,
		`{"title":"admin managed"}`, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("admin update = %d; body=%s", rec.Code, rec.Body.String())
	}
	moderatorUpdateID := createVideo(t, srv, ownerTok, "bob", `{"title":"moderator old","privacy":"private"}`)
	buf.Reset()
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+moderatorUpdateID,
		`{"title":"moderator managed"}`, moderatorTok); rec.Code != http.StatusOK {
		t.Fatalf("moderator update = %d; body=%s", rec.Code, rec.Body.String())
	}
	updateAudit := findAudit(auditEvents(t, &buf), observability.ActionVideoUpdate, observability.ResultSuccess)
	if updateAudit == nil {
		t.Fatal("managed update emitted no video-update audit event")
	}
	if updateAudit["actor_id"] != moderatorID {
		t.Errorf("managed update actor_id = %v, want moderator %s", updateAudit["actor_id"], moderatorID)
	}
	if reason, _ := updateAudit["reason"].(string); !strings.Contains(reason, "video="+moderatorUpdateID) || !strings.Contains(reason, "fields=title") || strings.Contains(reason, "moderator managed") {
		t.Errorf("managed update reason = %q, want target and field name only", reason)
	}

	// The Manage deep link can load metadata for every local visibility state.
	// Ordinary viewers keep the existing 404 behavior.
	assertManagedDetail := func(label, id string) {
		t.Helper()
		if rec := getVideo(srv, id, regularTok); rec.Code != http.StatusNotFound {
			t.Errorf("regular detail of %s = %d, want 404", label, rec.Code)
		}
		if rec := getVideo(srv, id, adminTok); rec.Code != http.StatusOK {
			t.Errorf("admin detail of %s = %d, want 200; body=%s", label, rec.Code, rec.Body.String())
		}
		if rec := getVideo(srv, id, moderatorTok); rec.Code != http.StatusOK {
			t.Errorf("moderator detail of %s = %d, want 200; body=%s", label, rec.Code, rec.Body.String())
		}
	}
	assertManagedDetail("private video", adminUpdateID)
	privateMediaID := createVideo(t, srv, ownerTok, "bob", `{"title":"private media","privacy":"private"}`)
	if rec := uploadVideoFile(srv, privateMediaID, "private.mp4", "video/mp4", "media", ownerTok); rec.Code != http.StatusCreated {
		t.Fatalf("upload private media fixture = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := getWithAuth(srv, "/api/v1/videos/"+privateMediaID+"/original", ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("owner private media = %d, want 200", rec.Code)
	}
	for label, token := range map[string]string{"admin": adminTok, "moderator": moderatorTok} {
		if rec := getWithAuth(srv, "/api/v1/videos/"+privateMediaID+"/original", token); rec.Code != http.StatusNotFound {
			t.Errorf("%s private media = %d, want 404 (metadata exception must not widen media)", label, rec.Code)
		}
	}

	stateVideo := func(title, state string) string {
		id := createVideo(t, srv, ownerTok, "bob", `{"title":"`+title+`","privacy":"public"}`)
		videoID := uuid.MustParse(id)
		row := repo.videos[videoID]
		row.State = state
		repo.videos[videoID] = row
		return id
	}
	assertManagedDetail("scheduled video", stateVideo("scheduled detail", "scheduled"))
	assertManagedDetail("quarantined video", stateVideo("quarantined detail", "quarantined"))
	blockedID := stateVideo("blocked detail", "published")
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+blockedID+"/block",
		`{"reason":"review"}`, adminTok); rec.Code != http.StatusNoContent {
		t.Fatalf("block detail fixture = %d; body=%s", rec.Code, rec.Body.String())
	}
	assertManagedDetail("blocked video", blockedID)

	regularDeniedID := createVideo(t, srv, ownerTok, "bob", `{"title":"owner only","privacy":"private"}`)
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+regularDeniedID,
		`{"title":"hax"}`, regularTok); rec.Code != http.StatusNotFound {
		t.Errorf("regular non-owner update = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+regularDeniedID, "", regularTok); rec.Code != http.StatusNotFound {
		t.Errorf("regular non-owner delete = %d, want 404", rec.Code)
	}

	adminDeleteID := createVideo(t, srv, ownerTok, "bob", `{"title":"admin delete","privacy":"private"}`)
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+adminDeleteID, "", adminTok); rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete = %d; body=%s", rec.Code, rec.Body.String())
	}

	moderatorDeleteID := createVideo(t, srv, ownerTok, "bob", `{"title":"moderator delete","privacy":"private"}`)
	buf.Reset() // isolate the managed-delete audit event asserted below
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+moderatorDeleteID, "", moderatorTok); rec.Code != http.StatusNoContent {
		t.Fatalf("moderator delete = %d; body=%s", rec.Code, rec.Body.String())
	}
	ev := findAudit(auditEvents(t, &buf), observability.ActionVideoDelete, observability.ResultSuccess)
	if ev == nil {
		t.Fatal("managed delete emitted no video-delete audit event")
	}
	if ev["actor_id"] != moderatorID {
		t.Errorf("managed delete actor_id = %v, want moderator %s", ev["actor_id"], moderatorID)
	}
}

func TestDeleteChannelEmitsAudit(t *testing.T) {
	srv := channelServer(t)
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))

	ownerTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	_ = postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes"}`, ownerTok)
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/channels/ada_makes", "", ownerTok); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", rec.Code)
	}

	if findAudit(auditEvents(t, &buf), observability.ActionChannelDelete, observability.ResultSuccess) == nil {
		t.Fatalf("no %s audit event emitted on channel delete", observability.ActionChannelDelete)
	}
}
