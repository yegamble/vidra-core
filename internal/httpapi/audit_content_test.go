package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"testing"

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
