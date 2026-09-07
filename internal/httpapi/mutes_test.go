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
	auth *authFakeRepo
	// channels mirrors the ARRAY() subquery on the list: the account's own
	// channel handles. Nil means "no channel fake wired" — the harnesses that
	// never create one then read an empty array, exactly as the SQL would.
	channels *channelFakeRepo
	mutes    []muteRow
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

// isMuted reports whether muter has muted muted (used by sibling fakes that
// mirror the real query's mute-filtering join).
func (f *muteFakeRepo) isMuted(muter, muted uuid.UUID) bool {
	for _, m := range f.mutes {
		if m.muter == muter && m.muted == muted {
			return true
		}
	}
	return false
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
			ChannelHandles: f.channels.handlesOwnedBy(m.muted),
		})
	}
	off := min(int(a.ResultOffset), len(rows))
	rows = rows[off:]
	if a.ResultLimit > 0 && int(a.ResultLimit) < len(rows) {
		rows = rows[:a.ResultLimit]
	}
	return rows, nil
}

// registerAndUser registers an account and returns its (token, user id). See
// registerTokens for the empty-instance owner-claim routing.
func registerAndUser(t *testing.T, srv *Server, body string) (string, string) {
	t.Helper()
	ar := registerTokens(t, srv, body)
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

func (f *muteFakeRepo) CountMutedAccounts(ctx context.Context, muterID uuid.UUID) (int64, error) {
	rows, err := f.ListMutedAccounts(ctx, sqlcgen.ListMutedAccountsParams{MuterID: muterID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

// TestMuteAndBlockListsCarryChannelHandles pins the one field the autosuggest
// half of the A16 ruling depends on. Autosuggest stays viewer-agnostic on the
// server — vidra-search's index stores static eligibility and never per-viewer
// state, which is exactly what makes the ranked-ids contract visibility-safe —
// so the frontend has to drop a channel suggestion naming a muted or blocked
// account itself. A suggestion carries only `channel_handle`, and these two
// lists carried only `user_id`/`username`: with nothing joining them, a client
// could only resolve the owner of each suggested handle with a request per
// keystroke. The lists now carry the account's channel handles, so the client
// builds the set ONCE per settled session and every keystroke is free.
//
// An account with no channel reads `[]`, never null: a client that has to
// null-check a set it intersects against would be one missed check away from
// showing what the mute hides.
func TestMuteAndBlockListsCarryChannelHandles(t *testing.T) {
	srv := videoServer(t)
	viewer, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bobTok, bobID := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	// Two channels, because one account may publish under several handles and
	// filtering on only the first would leak the rest.
	for _, h := range []string{"bobmain", "bobalt"} {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels", `{"handle":"`+h+`","display_name":"Bob"}`, bobTok); rec.Code != http.StatusCreated {
			t.Fatalf("create channel %s = %d; body=%s", h, rec.Code, rec.Body.String())
		}
	}
	// A channel-less account: the empty-array case.
	_, carolID := registerAndUser(t, srv, `{"username":"carol","email":"carol@example.test","password":"supersecret"}`)

	handlesOf := func(path string, decode func([]byte) map[string][]string) map[string][]string {
		t.Helper()
		rec := getWithAuth(srv, path, viewer)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d; body=%s", path, rec.Code, rec.Body.String())
		}
		return decode(rec.Body.Bytes())
	}
	// Decoded off the wire, not off the Go view struct: the field the frontend
	// reads is the JSON one, and `omitempty` on a nil slice would drop it.
	decodeMutes := func(b []byte) map[string][]string {
		var body struct {
			Accounts []struct {
				UserID         string   `json:"user_id"`
				ChannelHandles []string `json:"channel_handles"`
			} `json:"accounts"`
		}
		_ = json.Unmarshal(b, &body)
		out := map[string][]string{}
		for _, a := range body.Accounts {
			out[a.UserID] = a.ChannelHandles
		}
		return out
	}
	decodeBlocks := func(b []byte) map[string][]string {
		var body struct {
			Users []struct {
				UserID         string   `json:"user_id"`
				ChannelHandles []string `json:"channel_handles"`
			} `json:"users"`
		}
		_ = json.Unmarshal(b, &body)
		out := map[string][]string{}
		for _, u := range body.Users {
			out[u.UserID] = u.ChannelHandles
		}
		return out
	}

	for _, tc := range []struct {
		what   string
		mutate string
		list   string
		decode func([]byte) map[string][]string
	}{
		{"mute", "/api/v1/me/mutes/accounts/", "/api/v1/me/mutes/accounts", decodeMutes},
		{"block", "/api/v1/me/blocks/", "/api/v1/me/blocks", decodeBlocks},
	} {
		for _, id := range []string{bobID, carolID} {
			if rec := sendJSONAuth(srv, http.MethodPost, tc.mutate+id, "", viewer); rec.Code != http.StatusNoContent {
				t.Fatalf("%s %s = %d; body=%s", tc.what, id, rec.Code, rec.Body.String())
			}
		}
		got := handlesOf(tc.list, tc.decode)
		want := []string{"bobalt", "bobmain"} // sorted, so the set is stable
		if len(got[bobID]) != 2 || got[bobID][0] != want[0] || got[bobID][1] != want[1] {
			t.Errorf("%s list channel_handles for bob = %v, want %v", tc.what, got[bobID], want)
		}
		if got[carolID] == nil || len(got[carolID]) != 0 {
			t.Errorf("%s list channel_handles for a channel-less account = %v, want an empty array", tc.what, got[carolID])
		}
	}
}
