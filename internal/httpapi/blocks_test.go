package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
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
	auth   *authFakeRepo
	blocks []blockRow
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
		})
	}
	return rows, nil
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
