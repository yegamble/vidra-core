package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/video"
)

type moveHookCall struct {
	videoID      uuid.UUID
	wasFederated bool
}

// channelIDOf resolves a handle to its channel id through the public read.
func channelIDOf(t *testing.T, srv *Server, handle string) string {
	t.Helper()
	rec := getWithAuth(srv, "/api/v1/channels/"+handle, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get channel %s = %d; body=%s", handle, rec.Code, rec.Body.String())
	}
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || v.ID == "" {
		t.Fatalf("channel %s body = %s", handle, rec.Body.String())
	}
	return v.ID
}

// TestMoveVideoBetweenChannels is the T20 authZ matrix: the caller must manage
// BOTH channels (owner or editor, migration 0097); staff get no extra reach
// over the target; a missing and a foreign target answer the same 404; the same
// channel is a no-op. The update hook is the seam search reindexing, the
// federation Update and the IPFS sync all hang off, so capturing it proves each
// of them is driven by a move.
func TestMoveVideoBetweenChannels(t *testing.T) {
	var calls []moveHookCall
	srv, _, _, _, repo := videoServerFull(t, testConfig(), video.WithUpdateHook(
		func(_ context.Context, id uuid.UUID, was bool) { calls = append(calls, moveHookCall{id, was}) }))
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))

	adminTok := createChannelFor(t, srv, "root", "root@example.test", "root") // first user = admin
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	if rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada2","display_name":"ada2"}`, ownerTok); rec.Code != http.StatusCreated {
		t.Fatalf("create ada2 = %d; body=%s", rec.Code, rec.Body.String())
	}
	bobTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	editorTok, _ := inviteEditor(t, srv, ownerTok, "ada", "eve", "eve@example.test")
	strangerTok := registerAndToken(t, srv, `{"username":"carol","email":"carol@example.test","password":"supersecret"}`)
	ada, ada2, bob := channelIDOf(t, srv, "ada"), channelIDOf(t, srv, "ada2"), channelIDOf(t, srv, "bob")

	id := createVideo(t, srv, ownerTok, "ada", `{"title":"Secret Title","privacy":"public"}`)
	vid := uuid.MustParse(id)
	row := repo.videos[vid]
	row.State = "published"
	repo.videos[vid] = row
	move := func(tok, target string) *httptest.ResponseRecorder {
		return sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+id, `{"channel_id":"`+target+`"}`, tok)
	}
	channelOf := func() string { return repo.videos[vid].ChannelID.String() }

	// Malformed id -> 422.
	if rec := move(ownerTok, "nope"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("malformed channel_id = %d, want 422", rec.Code)
	}
	// Non-member of the source: 404, nothing moves.
	if rec := move(strangerTok, ada2); rec.Code != http.StatusNotFound {
		t.Errorf("stranger move = %d, want 404", rec.Code)
	}
	// A user who manages neither channel: 404.
	if rec := move(bobTok, ada2); rec.Code != http.StatusNotFound {
		t.Errorf("bob (foreign source) move = %d, want 404", rec.Code)
	}
	// Target the caller does not manage, and one that does not exist: same 404.
	if rec := move(ownerTok, bob); rec.Code != http.StatusNotFound {
		t.Errorf("owner -> foreign target = %d, want 404", rec.Code)
	}
	if rec := move(ownerTok, uuid.NewString()); rec.Code != http.StatusNotFound {
		t.Errorf("owner -> missing target = %d, want 404", rec.Code)
	}
	// An editor of the source only: the target is not theirs.
	if rec := move(editorTok, ada2); rec.Code != http.StatusNotFound {
		t.Errorf("editor of source only = %d, want 404", rec.Code)
	}
	// Admin: manages the video via the moderation escape but NOT the target.
	if rec := move(adminTok, ada2); rec.Code != http.StatusNotFound {
		t.Errorf("admin -> channel they do not manage = %d, want 404", rec.Code)
	}
	if got := channelOf(); got != ada || len(calls) != 0 {
		t.Fatalf("a refused move changed state: channel=%s hookCalls=%d", got, len(calls))
	}

	// Same channel is a no-op 200 (and no audit move event).
	buf.Reset()
	if rec := move(ownerTok, ada); rec.Code != http.StatusOK {
		t.Fatalf("same-channel move = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if findAudit(auditEvents(t, &buf), observability.ActionVideoMove, observability.ResultSuccess) != nil {
		t.Error("same-channel move emitted a move audit event")
	}

	// Owner -> own second channel.
	calls, buf = nil, bytes.Buffer{}
	rec := move(ownerTok, ada2)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner move = %d; body=%s", rec.Code, rec.Body.String())
	}
	var view videoView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view.ChannelID != ada2 || channelOf() != ada2 {
		t.Errorf("after move channel = %s / %s, want %s", view.ChannelID, channelOf(), ada2)
	}
	if len(calls) != 1 || calls[0].videoID != vid || !calls[0].wasFederated {
		t.Errorf("update hook calls = %+v, want one for the video with wasFederated=true", calls)
	}
	ev := findAudit(auditEvents(t, &buf), observability.ActionVideoMove, observability.ResultSuccess)
	if ev == nil {
		t.Fatal("no move audit event")
	}
	reason, _ := ev["reason"].(string)
	want := "video=" + id + " from=" + ada + " to=" + ada2
	if reason != want {
		t.Errorf("audit reason = %q, want %q", reason, want)
	}
	if bytes.Contains(buf.Bytes(), []byte("Secret Title")) {
		t.Error("audit event leaked the title")
	}

	// An editor of BOTH channels may move it back.
	if rec := postJSONAuth(srv, "/api/v1/channels/ada2/members", `{"handle":"eve"}`, ownerTok); rec.Code != http.StatusCreated {
		t.Fatalf("invite editor to ada2 = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := move(editorTok, ada); rec.Code != http.StatusOK || channelOf() != ada {
		t.Fatalf("editor-of-both move = %d channel=%s; body=%s", rec.Code, channelOf(), rec.Body.String())
	}

	// Admin who manages the target: allowed (mirrors the edit path, no widening).
	// bob invites root as editor of his channel; root moves nothing of bob's here,
	// but root -> ada's video needs root to manage `ada`'s video (staff) AND the
	// target: make root an editor of ada2.
	if rec := postJSONAuth(srv, "/api/v1/channels/ada2/members", `{"handle":"root"}`, ownerTok); rec.Code != http.StatusCreated {
		t.Fatalf("invite admin = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := move(adminTok, ada2); rec.Code != http.StatusOK || channelOf() != ada2 {
		t.Errorf("admin who manages target = %d channel=%s; body=%s", rec.Code, channelOf(), rec.Body.String())
	}
}

// TestMoveVideoSameOwnerOnly: a move is allowed only between channels with the
// SAME owner, whoever asks. Every caller below manages BOTH channels, so only the
// service's same-owner guard can refuse them (the handler's manage-both check
// passes); a refusal writes no move audit event, fires no hook and leaves the
// video where it was.
func TestMoveVideoSameOwnerOnly(t *testing.T) {
	var calls []moveHookCall
	var moves int
	srv, _, _, _, repo := videoServerFull(t, testConfig(),
		video.WithUpdateHook(func(_ context.Context, id uuid.UUID, was bool) { calls = append(calls, moveHookCall{id, was}) }),
		video.WithMoveHook(func(context.Context, uuid.UUID, uuid.UUID, bool) { moves++ }))
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))

	adminTok := createChannelFor(t, srv, "root", "root@example.test", "root") // first user = admin
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	editorTok := createChannelFor(t, srv, "eve", "eve@example.test", "eve") // owns channel "eve"
	// eve is an editor of ada; ada is an editor of eve; root is an editor of ada.
	for _, m := range []struct{ tok, ch, user string }{
		{ownerTok, "ada", "eve"}, {editorTok, "eve", "ada"}, {ownerTok, "ada", "root"},
	} {
		if rec := postJSONAuth(srv, "/api/v1/channels/"+m.ch+"/members", `{"handle":"`+m.user+`"}`, m.tok); rec.Code != http.StatusCreated {
			t.Fatalf("invite %s to %s = %d; body=%s", m.user, m.ch, rec.Code, rec.Body.String())
		}
	}
	ada, eve, root := channelIDOf(t, srv, "ada"), channelIDOf(t, srv, "eve"), channelIDOf(t, srv, "root")

	adaVideo := createVideo(t, srv, ownerTok, "ada", `{"title":"ada clip","privacy":"public"}`)
	eveVideo := createVideo(t, srv, editorTok, "eve", `{"title":"eve clip","privacy":"public"}`)
	for _, c := range []struct {
		name, tok, video, target, stays string
	}{
		{"editor moves owner's video into the editor's own channel", editorTok, adaVideo, eve, ada},
		{"editor pushes their own video into the owner's channel", editorTok, eveVideo, ada, eve},
		{"staff moves into their own channel", adminTok, adaVideo, root, ada},
		{"owner moves into a channel of an editor they share", ownerTok, adaVideo, eve, ada},
	} {
		buf.Reset()
		rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+c.video, `{"channel_id":"`+c.target+`"}`, c.tok)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404; body=%s", c.name, rec.Code, rec.Body.String())
		}
		if got := repo.videos[uuid.MustParse(c.video)].ChannelID.String(); got != c.stays {
			t.Errorf("%s: video moved to %s", c.name, got)
		}
		if findAudit(auditEvents(t, &buf), observability.ActionVideoMove, observability.ResultSuccess) != nil {
			t.Errorf("%s: a move audit event was written", c.name)
		}
	}
	if len(calls) != 0 || moves != 0 {
		t.Errorf("refused moves fired hooks: update=%d move=%d", len(calls), moves)
	}
}
