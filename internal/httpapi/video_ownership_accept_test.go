package httpapi

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/video"
)

func answerOwnership(srv *Server, tok, changeID, verb, body string) *httptest.ResponseRecorder {
	return postJSONAuth(srv, "/api/v1/me/video-ownership-changes/"+changeID+"/"+verb, body, tok)
}

// TestAcceptVideoOwnership is the accept matrix (T21). Only the recipient
// answers, only a pending request, only into a channel they own, only within
// their storage quota, and only while the giver still owns the video. A
// successful accept is the same channel move a same-owner move is: the update
// hooks (search reindex, federation Update, IPFS sync) and the move hooks
// (federation retract to the old channel's followers) fire once each.
func TestAcceptVideoOwnership(t *testing.T) {
	var updates []moveHookCall
	var moves []uuid.UUID
	srv, repo, own := ownershipServer(t,
		video.WithUpdateHook(func(_ context.Context, id uuid.UUID, was bool) { updates = append(updates, moveHookCall{id, was}) }),
		video.WithMoveHook(func(_ context.Context, _ uuid.UUID, from uuid.UUID, _ bool) { moves = append(moves, from) }))
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))
	adaTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	if rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"bob2","display_name":"bob2"}`, bobTok); rec.Code != http.StatusCreated {
		t.Fatalf("create bob2 = %d; body=%s", rec.Code, rec.Body.String())
	}
	carolTok := createChannelFor(t, srv, "carol", "carol@example.test", "carol")
	ada, bob2, carol := channelIDOf(t, srv, "ada"), channelIDOf(t, srv, "bob2"), channelIDOf(t, srv, "carol")
	id := createPublishedVideo(t, srv, adaTok, "ada", `{"title":"Gift","privacy":"public"}`)
	vid := uuid.MustParse(id)
	if rec := giveOwnership(srv, adaTok, id, "bob"); rec.Code != http.StatusNoContent {
		t.Fatalf("give = %d; body=%s", rec.Code, rec.Body.String())
	}
	change := own.rows[0].ID.String()
	accept := func(tok, channel string) *httptest.ResponseRecorder {
		return answerOwnership(srv, tok, change, "accept", `{"channel_id":"`+channel+`"}`)
	}
	expect := func(name string, rec *httptest.ResponseRecorder, code int) {
		t.Helper()
		if rec.Code != code {
			t.Errorf("%s = %d, want %d; body=%s", name, rec.Code, code, rec.Body.String())
		}
	}

	expect("stranger accept", accept(carolTok, carol), http.StatusNotFound)
	expect("giver accept", accept(adaTok, ada), http.StatusNotFound)
	expect("unknown request", answerOwnership(srv, bobTok, uuid.NewString(), "accept", `{"channel_id":"`+bob2+`"}`), http.StatusNotFound)
	expect("malformed channel_id", accept(bobTok, "nope"), http.StatusUnprocessableEntity)
	expect("someone else's channel", accept(bobTok, carol), http.StatusNotFound)
	expect("missing channel", accept(bobTok, uuid.NewString()), http.StatusNotFound)

	// Over quota: the video's 4 stored bytes do not fit a 3-byte quota.
	bob := repo.users.users["bob@example.test"]
	quota := int64(3)
	bob.StorageQuotaBytes = &quota
	repo.users.users["bob@example.test"] = bob
	expect("over quota", accept(bobTok, bob2), http.StatusUnprocessableEntity)
	bob.StorageQuotaBytes = nil
	repo.users.users["bob@example.test"] = bob

	// The giver lost the video between give and accept: refused, nothing moves.
	row := repo.videos[vid]
	row.OwnerID = repo.users.users["carol@example.test"].ID
	repo.videos[vid] = row
	expect("giver no longer owns it", accept(bobTok, bob2), http.StatusConflict)
	row.OwnerID = repo.users.users["ada@example.test"].ID
	repo.videos[vid] = row

	if repo.videos[vid].ChannelID.String() != ada || own.rows[0].Status != "pending" || len(updates)+len(moves) != 0 {
		t.Fatalf("a refused accept changed state: channel=%s status=%s hooks=%d/%d",
			repo.videos[vid].ChannelID, own.rows[0].Status, len(updates), len(moves))
	}

	buf.Reset()
	expect("accept", accept(bobTok, bob2), http.StatusNoContent)
	if got := repo.videos[vid]; got.ChannelID.String() != bob2 || got.OwnerID != bob.ID {
		t.Errorf("after accept: channel=%s owner=%s, want bob2/bob", got.ChannelID, got.OwnerID)
	}
	if own.rows[0].Status != "accepted" {
		t.Errorf("status = %q, want accepted", own.rows[0].Status)
	}
	if len(updates) != 1 || updates[0].videoID != vid || !updates[0].wasFederated || len(moves) != 1 || moves[0].String() != ada {
		t.Errorf("hooks: update=%+v move=%v, want one each (move from ada)", updates, moves)
	}
	ev := findAudit(auditEvents(t, &buf), observability.ActionVideoMove, observability.ResultSuccess)
	if reason, _ := ev["reason"].(string); reason != "video="+id+" from="+ada+" to="+bob2 {
		t.Errorf("move audit reason = %q", reason)
	}
	expect("second accept", accept(bobTok, bob2), http.StatusConflict)
	expect("refuse after accept", answerOwnership(srv, bobTok, change, "refuse", ""), http.StatusConflict)
}

// TestRefuseVideoOwnership: only the recipient refuses, only once; the video
// stays exactly where it was (no hooks), and the giver may offer it again
// (to the refuser only after the cooldown).
func TestRefuseVideoOwnership(t *testing.T) {
	var hooks int
	srv, repo, own := ownershipServer(t,
		video.WithUpdateHook(func(context.Context, uuid.UUID, bool) { hooks++ }),
		video.WithMoveHook(func(context.Context, uuid.UUID, uuid.UUID, bool) { hooks++ }))
	adaTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	carolTok := createChannelFor(t, srv, "carol", "carol@example.test", "carol")
	id := createPublishedVideo(t, srv, adaTok, "ada", `{"title":"Gift"}`)
	if rec := giveOwnership(srv, adaTok, id, "bob"); rec.Code != http.StatusNoContent {
		t.Fatalf("give = %d; body=%s", rec.Code, rec.Body.String())
	}
	change := own.rows[0].ID.String()
	before := repo.videos[uuid.MustParse(id)]

	for _, tok := range []string{carolTok, adaTok} {
		if rec := answerOwnership(srv, tok, change, "refuse", ""); rec.Code != http.StatusNotFound {
			t.Errorf("non-recipient refuse = %d, want 404", rec.Code)
		}
	}
	if rec := answerOwnership(srv, bobTok, change, "refuse", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("refuse = %d; body=%s", rec.Code, rec.Body.String())
	}
	if after := repo.videos[uuid.MustParse(id)]; after.ChannelID != before.ChannelID || after.OwnerID != before.OwnerID || hooks != 0 {
		t.Errorf("refuse moved the video or fired hooks: %s -> %s, hooks=%d", before.ChannelID, after.ChannelID, hooks)
	}
	if own.rows[0].Status != "refused" {
		t.Errorf("status = %q, want refused", own.rows[0].Status)
	}
	if rec := answerOwnership(srv, bobTok, change, "refuse", ""); rec.Code != http.StatusConflict {
		t.Errorf("second refuse = %d, want 409", rec.Code)
	}
	// The video can be offered again — to someone else straight away; the same
	// recipient only after the refusal cooldown.
	if rec := giveOwnership(srv, adaTok, id, "bob"); rec.Code != http.StatusConflict {
		t.Errorf("re-offer to the refuser = %d, want 409 (cooldown)", rec.Code)
	}
	if rec := giveOwnership(srv, adaTok, id, "carol"); rec.Code != http.StatusNoContent {
		t.Errorf("offer to someone else after a refusal = %d, want 204", rec.Code)
	}
}
