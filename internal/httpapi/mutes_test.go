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

// muteFakeRepo is an in-memory mute.Repository. It resolves the muted account's
// identity from the auth fake (mirroring the real JOIN) and enforces the target
// foreign key (an unknown muted account → 23503).
type muteFakeRepo struct {
	auth  *authFakeRepo
	mutes []muteRow
}

type muteRow struct {
	muter uuid.UUID
	muted uuid.UUID
	at    time.Time
}

func (f *muteFakeRepo) MuteAccount(_ context.Context, a sqlcgen.MuteAccountParams) (int64, error) {
	if _, err := f.auth.GetUserByID(context.Background(), a.MutedID); err != nil {
		return 0, &pgconn.PgError{Code: "23503"} // FK violation: no such user
	}
	for _, m := range f.mutes {
		if m.muter == a.MuterID && m.muted == a.MutedID {
			return 0, nil // already muted (idempotent)
		}
	}
	f.mutes = append(f.mutes, muteRow{muter: a.MuterID, muted: a.MutedID, at: time.Now()})
	return 1, nil
}

func (f *muteFakeRepo) UnmuteAccount(_ context.Context, a sqlcgen.UnmuteAccountParams) (int64, error) {
	for i, m := range f.mutes {
		if m.muter == a.MuterID && m.muted == a.MutedID {
			f.mutes = append(f.mutes[:i], f.mutes[i+1:]...)
			return 1, nil
		}
	}
	return 0, nil
}

func (f *muteFakeRepo) ListMutedAccounts(_ context.Context, a sqlcgen.ListMutedAccountsParams) ([]sqlcgen.ListMutedAccountsRow, error) {
	var rows []sqlcgen.ListMutedAccountsRow
	for i := len(f.mutes) - 1; i >= 0; i-- { // newest mute first
		m := f.mutes[i]
		if m.muter != a.MuterID {
			continue
		}
		u, err := f.auth.GetUserByID(context.Background(), m.muted)
		if err != nil {
			continue
		}
		rows = append(rows, sqlcgen.ListMutedAccountsRow{
			MutedID: m.muted, Username: u.Username, DisplayName: u.DisplayName, CreatedAt: m.at,
		})
	}
	off := min(int(a.ResultOffset), len(rows))
	rows = rows[off:]
	if a.ResultLimit > 0 && int(a.ResultLimit) < len(rows) {
		rows = rows[:a.ResultLimit]
	}
	return rows, nil
}

// registerAndUser registers an account and returns its (token, user id).
func registerAndUser(t *testing.T, srv *Server, body string) (string, string) {
	t.Helper()
	rec := postTo(srv, "/api/v1/auth/register", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register = %d; body=%s", rec.Code, rec.Body.String())
	}
	var ar authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ar); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return ar.Token, ar.User.ID
}

// TestMuteAccountFlow covers the mute → list → unmute round trip, including
// idempotency.
func TestMuteAccountFlow(t *testing.T) {
	srv := videoServer(t)
	muter, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	_, targetID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Initially empty.
	var empty mutedAccountListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/me/mutes/accounts", muter).Body.Bytes(), &empty)
	if len(empty.Accounts) != 0 {
		t.Fatalf("muted before mute = %d, want 0", len(empty.Accounts))
	}

	// Mute bob, twice (idempotent).
	for i := 0; i < 2; i++ {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/mutes/accounts/"+targetID, "", muter); rec.Code != http.StatusNoContent {
			t.Fatalf("mute #%d = %d; body=%s", i, rec.Code, rec.Body.String())
		}
	}

	// List shows bob once, with identity.
	var body mutedAccountListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/me/mutes/accounts", muter).Body.Bytes(), &body)
	if len(body.Accounts) != 1 || body.Accounts[0].UserID != targetID || body.Accounts[0].Username != "bob" {
		t.Fatalf("muted = %+v, want one bob (%s)", body.Accounts, targetID)
	}

	// Unmute (twice; idempotent) → empty again.
	for i := 0; i < 2; i++ {
		if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/mutes/accounts/"+targetID, "", muter); rec.Code != http.StatusNoContent {
			t.Fatalf("unmute #%d = %d", i, rec.Code)
		}
	}
	var after mutedAccountListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/me/mutes/accounts", muter).Body.Bytes(), &after)
	if len(after.Accounts) != 0 {
		t.Errorf("muted after unmute = %d, want 0", len(after.Accounts))
	}
}

// TestMuteAccountSelfUnknownAndAuth covers the self-mute, unknown-target, and
// unauthenticated cases.
func TestMuteAccountSelfUnknownAndAuth(t *testing.T) {
	srv := videoServer(t)
	tok, selfID := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Muting yourself → 422.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/mutes/accounts/"+selfID, "", tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("self-mute = %d, want 422", rec.Code)
	}
	// Muting an unknown account → 404.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/mutes/accounts/"+uuid.New().String(), "", tok); rec.Code != http.StatusNotFound {
		t.Errorf("mute unknown = %d, want 404", rec.Code)
	}

	// Auth required on all three routes.
	someID := uuid.New().String()
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/me/mutes/accounts"},
		{http.MethodPost, "/api/v1/me/mutes/accounts/" + someID},
		{http.MethodDelete, "/api/v1/me/mutes/accounts/" + someID},
	}
	for _, tc := range cases {
		if rec := sendJSONAuth(srv, tc.method, tc.path, "", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("anon %s %s = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}
