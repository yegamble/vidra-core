package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
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

func (f *contentPrefsFake) UpsertUserContentPrefs(_ context.Context, a sqlcgen.UpsertUserContentPrefsParams) (sqlcgen.UserContentPref, error) {
	// Mirrors the SQL: a NULL argument keeps the stored value (empty on first save).
	r, ok := f.rows[a.UserID]
	if !ok {
		r = sqlcgen.UserContentPref{UserID: a.UserID, SensitiveFlagPolicies: []byte("{}"), PreferredLanguages: []string{}}
	}
	if a.SensitiveFlagPolicies != nil {
		r.SensitiveFlagPolicies = a.SensitiveFlagPolicies
	}
	if a.PreferredLanguages != nil {
		r.PreferredLanguages = a.PreferredLanguages
	}
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

// The viewer's preferred languages ride the feed's existing ?language param as
// a comma-separated list; a video matches ANY of them.
func TestPreferredLanguagesFilterTheFeed(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	for _, l := range []string{"en", "fr", "de"} {
		createPublishedVideo(t, srv, tok, "ada", `{"title":"clip `+l+`","privacy":"public","language":"`+l+`"}`)
	}
	titles := func(query string) string {
		t.Helper()
		rec := getPath(srv, "/api/v1/videos"+query)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d; body=%s", query, rec.Code, rec.Body.String())
		}
		var got []string
		for _, c := range decodeCards(t, rec.Body.Bytes()) {
			got = append(got, c["title"].(string))
		}
		sort.Strings(got)
		return strings.Join(got, "|")
	}
	if got := titles("?language=en,fr"); got != "clip en|clip fr" {
		t.Errorf("language=en,fr = %q, want en and fr only", got)
	}
	if got := titles("?language=de"); got != "clip de" {
		t.Errorf("language=de = %q (the single-id form must keep working)", got)
	}
	if got := titles(""); got != "clip de|clip en|clip fr" {
		t.Errorf("no filter = %q, want all three", got)
	}
	if rec := getPath(srv, "/api/v1/videos?language=en,zz-nope"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown language in the list = %d, want 422", rec.Code)
	}

	// Saving preferred_languages does not disturb the flag overrides.
	put := func(body string) *httptest.ResponseRecorder {
		return sendJSONAuth(srv, http.MethodPut, "/api/v1/me/content-preferences", body, tok)
	}
	if rec := put(`{"sensitive_flag_policies":{"sex":"hide"}}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT flags = %d", rec.Code)
	}
	rec := put(`{"preferred_languages":["fr","en"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT languages = %d; body=%s", rec.Code, rec.Body.String())
	}
	var out contentPrefsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if strings.Join(out.PreferredLanguages, ",") != "fr,en" || out.SensitiveFlagPolicies["sex"] != "hide" {
		t.Errorf("saved = %+v, want languages [fr en] and sex=hide kept", out)
	}
	if rec := put(`{"preferred_languages":["klingon"]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown language PUT = %d, want 400", rec.Code)
	}
}
