package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// blockFakeRepo is an in-memory block.Repository. It resolves the blocked
// account's identity from the auth fake (mirroring the real JOIN) and enforces
// the target foreign key (an unknown blocked account → 23503).
type blockFakeRepo struct {
	auth *authFakeRepo
	// channels mirrors the ARRAY() subquery on the list — see muteFakeRepo.
	channels *channelFakeRepo
	blocks   []blockRow
}

type blockRow struct {
	blocker uuid.UUID
	blocked uuid.UUID
	at      time.Time
}

func (f *blockFakeRepo) BlockUser(_ context.Context, a sqlcgen.BlockUserParams) (int64, error) {
	if _, err := f.auth.GetUserByID(context.Background(), a.BlockedID); err != nil {
		return 0, &pgconn.PgError{Code: "23503"} // FK violation: no such user
	}
	for _, b := range f.blocks {
		if b.blocker == a.BlockerID && b.blocked == a.BlockedID {
			return 0, nil // already blocked (idempotent)
		}
	}
	f.blocks = append(f.blocks, blockRow{blocker: a.BlockerID, blocked: a.BlockedID, at: time.Now()})
	return 1, nil
}

func (f *blockFakeRepo) UnblockUser(_ context.Context, a sqlcgen.UnblockUserParams) (int64, error) {
	for i, b := range f.blocks {
		if b.blocker == a.BlockerID && b.blocked == a.BlockedID {
			f.blocks = append(f.blocks[:i], f.blocks[i+1:]...)
			return 1, nil
		}
	}
	return 0, nil
}

func (f *blockFakeRepo) ListBlockedUsers(_ context.Context, a sqlcgen.ListBlockedUsersParams) ([]sqlcgen.ListBlockedUsersRow, error) {
	var rows []sqlcgen.ListBlockedUsersRow
	for i := len(f.blocks) - 1; i >= 0; i-- { // newest block first
		b := f.blocks[i]
		if b.blocker != a.BlockerID {
			continue
		}
		u, err := f.auth.GetUserByID(context.Background(), b.blocked)
		if err != nil {
			continue
		}
		rows = append(rows, sqlcgen.ListBlockedUsersRow{
			BlockedID: b.blocked, Username: u.Username, DisplayName: u.DisplayName, CreatedAt: b.at,
			ChannelHandles: f.channels.handlesOwnedBy(b.blocked),
		})
	}
	return rows, nil
}

// isBlocked reports whether blocker has blocked blocked (one direction only —
// used by sibling fakes that mirror the §13 content-hiding NOT EXISTS, which
// keys on blocker = viewer, unlike the symmetric DM gate).
func (f *blockFakeRepo) isBlocked(blocker, blocked uuid.UUID) bool {
	for _, b := range f.blocks {
		if b.blocker == blocker && b.blocked == blocked {
			return true
		}
	}
	return false
}

func (f *blockFakeRepo) IsBlockedBetween(_ context.Context, a sqlcgen.IsBlockedBetweenParams) (bool, error) {
	for _, b := range f.blocks {
		if (b.blocker == a.BlockerID && b.blocked == a.BlockedID) ||
			(b.blocker == a.BlockedID && b.blocked == a.BlockerID) {
			return true, nil
		}
	}
	return false, nil
}

// TestFeedHidesBlockedAccounts mirrors TestFeedHidesMutedAccounts for §13:
// blocking a user hides their videos from the blocker's feed, search, and
// subscriptions — per-viewer, anonymous viewers unaffected — and unblocking
// restores them.
func TestFeedHidesBlockedAccounts(t *testing.T) {
	srv := videoServer(t)
	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	_ = createPublishedVideo(t, srv, ada, "ada", `{"title":"by ada","privacy":"public"}`)

	// A second creator, bob.
	bobTok, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels", `{"handle":"bob","display_name":"Bob"}`, bobTok); rec.Code != http.StatusCreated {
		t.Fatalf("create bob channel = %d; body=%s", rec.Code, rec.Body.String())
	}
	_ = createPublishedVideo(t, srv, bobTok, "bob", `{"title":"by bob","privacy":"public"}`)

	// A viewer, charlie, who follows bob (for the subscriptions feed).
	charlie := registerAndToken(t, srv, `{"username":"charlie","email":"charlie@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels/bob/follow", "", charlie); rec.Code != http.StatusNoContent {
		t.Fatalf("follow bob = %d", rec.Code)
	}

	titles := func(path, tok string, into any) []string {
		t.Helper()
		rec := sendJSONAuth(srv, http.MethodGet, path, "", tok)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d; body=%s", path, rec.Code, rec.Body.String())
		}
		var body struct {
			Videos []struct {
				Title string `json:"title"`
			} `json:"videos"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		out := make([]string, 0, len(body.Videos))
		for _, v := range body.Videos {
			out = append(out, v.Title)
		}
		return out
	}
	feed := func(tok string) []string { return titles("/api/v1/videos", tok, nil) }
	search := func(tok string) []string { return titles("/api/v1/videos/search?q=by", tok, nil) }
	subs := func(tok string) []string { return titles("/api/v1/me/subscriptions/videos", tok, nil) }

	// Before blocking, charlie sees both creators (and bob in subscriptions).
	if got := feed(charlie); len(got) != 2 {
		t.Fatalf("charlie feed before block = %v, want 2", got)
	}
	if got := subs(charlie); len(got) != 1 || got[0] != "by bob" {
		t.Fatalf("charlie subscriptions before block = %v, want [by bob]", got)
	}

	// charlie blocks bob.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/blocks/"+bobID, "", charlie); rec.Code != http.StatusNoContent {
		t.Fatalf("block bob = %d; body=%s", rec.Code, rec.Body.String())
	}

	// Feed, search, and subscriptions now exclude bob's video for charlie;
	// an anonymous viewer still sees both (blocks are per-viewer).
	if got := feed(charlie); len(got) != 1 || got[0] != "by ada" {
		t.Errorf("charlie feed after block = %v, want [by ada]", got)
	}
	if got := search(charlie); len(got) != 1 || got[0] != "by ada" {
		t.Errorf("charlie search after block = %v, want [by ada]", got)
	}
	if got := subs(charlie); len(got) != 0 {
		t.Errorf("charlie subscriptions after block = %v, want none", got)
	}
	if got := feed(""); len(got) != 2 {
		t.Errorf("anon feed = %v, want 2 (blocks are per-viewer)", got)
	}

	// Unblocking restores bob's video everywhere.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/blocks/"+bobID, "", charlie); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock = %d", rec.Code)
	}
	if got := feed(charlie); len(got) != 2 {
		t.Errorf("charlie feed after unblock = %v, want 2", got)
	}
	if got := subs(charlie); len(got) != 1 {
		t.Errorf("charlie subscriptions after unblock = %v, want 1", got)
	}
}

// TestCommentsHideBlockedAuthors mirrors TestCommentsHideMutedAuthors for §13:
// blocking a user hides their comments from the blocker (per-viewer; anonymous
// viewers unaffected), and unblocking restores them.
func TestCommentsHideBlockedAuthors(t *testing.T) {
	srv := videoServer(t)
	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, ada, "ada", `{"title":"v","privacy":"public"}`)
	bobTok, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	charlieTok, _ := registerAndUser(t, srv, `{"username":"charlie","email":"charlie@example.test","password":"supersecret"}`)

	parse := func(rec *httptest.ResponseRecorder) []commentView {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("list = %d; body=%s", rec.Code, rec.Body.String())
		}
		var body commentListResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body.Comments
	}

	// bob and charlie each comment on ada's video.
	for _, c := range []struct{ tok, body string }{{bobTok, "from bob"}, {charlieTok, "from charlie"}} {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/comments", `{"body":"`+c.body+`"}`, c.tok); rec.Code != http.StatusCreated {
			t.Fatalf("comment %q = %d; body=%s", c.body, rec.Code, rec.Body.String())
		}
	}

	// ada blocks bob.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/blocks/"+bobID, "", ada); rec.Code != http.StatusNoContent {
		t.Fatalf("block bob = %d; body=%s", rec.Code, rec.Body.String())
	}

	// ada (authenticated) no longer sees bob's comment; an anonymous viewer
	// still does — the filter is per-viewer, like mutes.
	adaSees := parse(getWithAuth(srv, "/api/v1/videos/"+vid+"/comments", ada))
	if len(adaSees) != 1 || adaSees[0].Body != "from charlie" {
		t.Fatalf("ada (blocked bob) sees %+v, want only [from charlie]", adaSees)
	}
	anonRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(anonRec, httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+vid+"/comments", nil))
	if anon := parse(anonRec); len(anon) != 2 {
		t.Errorf("anon sees %d comments, want 2 (blocks are per-viewer)", len(anon))
	}

	// Unblocking restores bob's comment for ada.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/blocks/"+bobID, "", ada); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock bob = %d", rec.Code)
	}
	if got := parse(getWithAuth(srv, "/api/v1/videos/"+vid+"/comments", ada)); len(got) != 2 {
		t.Errorf("ada after unblock sees %d comments, want 2", len(got))
	}
}

// TestBlockUserFlow covers block → list → unblock, plus self (422), unknown (404),
// and anonymous (401).
func TestBlockUserFlow(t *testing.T) {
	srv := videoServer(t)
	blocker, blockerID := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	_, targetID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Anonymous cannot block.
	if rec := postTo(srv, "/api/v1/me/blocks/"+targetID, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon block = %d, want 401", rec.Code)
	}
	// Blocking yourself → 422; an unknown target → 404.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/blocks/"+blockerID, "", blocker); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("self-block = %d, want 422", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/blocks/"+uuid.NewString(), "", blocker); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown-target block = %d, want 404", rec.Code)
	}

	// Block bob, twice (idempotent).
	for i := 0; i < 2; i++ {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/blocks/"+targetID, "", blocker); rec.Code != http.StatusNoContent {
			t.Fatalf("block #%d = %d; body=%s", i, rec.Code, rec.Body.String())
		}
	}

	// The block appears in the list.
	var list blockedUserListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/me/blocks", blocker).Body.Bytes(), &list)
	if len(list.Users) != 1 || list.Users[0].UserID != targetID || list.Users[0].Username != "bob" {
		t.Fatalf("blocks = %+v, want [bob]", list.Users)
	}

	// Unblock (idempotent) → list empty.
	for i := 0; i < 2; i++ {
		if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/blocks/"+targetID, "", blocker); rec.Code != http.StatusNoContent {
			t.Fatalf("unblock #%d = %d", i, rec.Code)
		}
	}
	var empty blockedUserListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/me/blocks", blocker).Body.Bytes(), &empty)
	if len(empty.Users) != 0 {
		t.Fatalf("blocks after unblock = %d, want 0", len(empty.Users))
	}
}

// TestMessagingBlockedByUserBlock proves a block gates direct messaging in both
// directions (403), and unblocking restores it.
func TestMessagingBlockedByUserBlock(t *testing.T) {
	srv := videoServer(t)
	ada, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bob, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Ada starts a conversation with Bob (no block yet).
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+bobID+`"}`, ada)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var conv conversationView
	_ = json.Unmarshal(rec.Body.Bytes(), &conv)

	// Ada blocks Bob.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/blocks/"+bobID, "", ada); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d, want 204", rec.Code)
	}

	// Now neither can send in the existing conversation (symmetric → 403).
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+conv.ID+"/messages", `{"body":"hi"}`, ada); rec.Code != http.StatusForbidden {
		t.Fatalf("ada send while blocking = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+conv.ID+"/messages", `{"body":"hi"}`, bob); rec.Code != http.StatusForbidden {
		t.Fatalf("bob send while blocked = %d, want 403", rec.Code)
	}
	// And starting (start-or-get) is refused too.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations", `{"recipient_id":"`+bobID+`"}`, ada); rec.Code != http.StatusForbidden {
		t.Fatalf("start while blocking = %d, want 403", rec.Code)
	}

	// Unblock → messaging works again.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/blocks/"+bobID, "", ada); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock = %d, want 204", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+conv.ID+"/messages", `{"body":"hi again"}`, ada); rec.Code != http.StatusCreated {
		t.Fatalf("ada send after unblock = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

func (f *blockFakeRepo) CountBlockedUsers(ctx context.Context, blockerID uuid.UUID) (int64, error) {
	rows, err := f.ListBlockedUsers(ctx, sqlcgen.ListBlockedUsersParams{BlockerID: blockerID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}
