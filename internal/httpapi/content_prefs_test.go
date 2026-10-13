package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type contentPrefsFake struct {
	rows map[uuid.UUID]sqlcgen.UserContentPref
}

func (f *contentPrefsFake) GetUserContentPrefs(_ context.Context, id uuid.UUID) (sqlcgen.UserContentPref, error) {
	if r, ok := f.rows[id]; ok {
		return r, nil
	}
	return sqlcgen.UserContentPref{}, pgx.ErrNoRows
}

func (f *contentPrefsFake) UpsertUserSensitiveFlagPolicies(_ context.Context, a sqlcgen.UpsertUserSensitiveFlagPoliciesParams) (sqlcgen.UserContentPref, error) {
	r := sqlcgen.UserContentPref{UserID: a.UserID, SensitiveFlagPolicies: a.SensitiveFlagPolicies}
	f.rows[a.UserID] = r
	return r, nil
}

func TestContentPreferencesRoundTrip(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/content-preferences", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET = %d, want 401", rec.Code)
	}
	read := func() map[string]string {
		t.Helper()
		rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/content-preferences", "", tok)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET = %d; body=%s", rec.Code, rec.Body.String())
		}
		var out contentPrefsResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.SensitiveFlagPolicies
	}
	if got := read(); got == nil || len(got) != 0 {
		t.Errorf("fresh user = %#v, want {}", got)
	}
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/me/content-preferences",
		`{"sensitive_flag_policies":{"violent":"blur","sex":"hide","shocking":""}}`, tok); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := read(); len(got) != 2 || got["violent"] != "blur" || got["sex"] != "hide" {
		t.Errorf("saved = %v, want violent=blur sex=hide (empty value inherits, not stored)", got)
	}
	for _, body := range []string{
		`{"sensitive_flag_policies":{"gore":"hide"}}`,
		`{"sensitive_flag_policies":{"sex":"nuke"}}`,
	} {
		if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/me/content-preferences", body, tok); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s = %d, want 400", body, rec.Code)
		}
	}
}
