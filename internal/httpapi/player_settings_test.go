package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// playerSettingsFakeRepo is an in-memory playersettings.Repository: one row per
// user, keyed by id. A miss returns pgx.ErrNoRows, mirroring the real :one query
// so the service serves defaults for a user who never saved.
type playerSettingsFakeRepo struct {
	rows map[uuid.UUID]sqlcgen.GetUserPlayerSettingsRow
}

func newPlayerSettingsFakeRepo() *playerSettingsFakeRepo {
	return &playerSettingsFakeRepo{rows: map[uuid.UUID]sqlcgen.GetUserPlayerSettingsRow{}}
}

func (f *playerSettingsFakeRepo) GetUserPlayerSettings(_ context.Context, userID uuid.UUID) (sqlcgen.GetUserPlayerSettingsRow, error) {
	row, ok := f.rows[userID]
	if !ok {
		return sqlcgen.GetUserPlayerSettingsRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *playerSettingsFakeRepo) UpsertUserPlayerSettings(_ context.Context, a sqlcgen.UpsertUserPlayerSettingsParams) error {
	if f.rows == nil {
		f.rows = map[uuid.UUID]sqlcgen.GetUserPlayerSettingsRow{}
	}
	f.rows[a.UserID] = sqlcgen.GetUserPlayerSettingsRow{
		UserID:          a.UserID,
		AutoplayNext:    a.AutoplayNext,
		DefaultSpeed:    a.DefaultSpeed,
		DefaultQuality:  a.DefaultQuality,
		CaptionsDefault: a.CaptionsDefault,
		TheaterDefault:  a.TheaterDefault,
		UpdatedAt:       time.Now(),
	}
	return nil
}

func getPlayerSettings(t *testing.T, srv *Server, token string) playerSettingsResponse {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/player-settings", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET player-settings = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body playerSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	return body
}

// TestPlayerSettingsDefaultsForFreshUser: a user who never saved gets the full
// default object on GET (200, never 404).
func TestPlayerSettingsDefaultsForFreshUser(t *testing.T) {
	srv := videoServer(t)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	got := getPlayerSettings(t, srv, tok)
	want := playerSettingsResponse{
		AutoplayNext:    true,
		DefaultSpeed:    1,
		DefaultQuality:  "auto",
		CaptionsDefault: false,
		TheaterDefault:  false,
	}
	if got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
}

// TestPlayerSettingsMergePut: a partial PUT changes only the supplied fields,
// returns the full effective object, and persists (a fresh GET agrees). A second
// partial PUT keeps the values set by the first.
func TestPlayerSettingsMergePut(t *testing.T) {
	srv := videoServer(t)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// First: set two fields; the other three keep their defaults.
	rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/me/player-settings",
		`{"default_speed":1.5,"captions_default":true}`, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body playerSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := playerSettingsResponse{
		AutoplayNext:    true, // default kept
		DefaultSpeed:    1.5,  // set
		DefaultQuality:  "auto",
		CaptionsDefault: true, // set
		TheaterDefault:  false,
	}
	if body != want {
		t.Fatalf("after PUT = %+v, want %+v", body, want)
	}
	// Persisted.
	if got := getPlayerSettings(t, srv, tok); got != want {
		t.Fatalf("fresh GET = %+v, want %+v", got, want)
	}

	// Second partial PUT: change quality only; speed/captions from the first PUT
	// must survive (merge, not replace-with-defaults).
	rec = sendJSONAuth(srv, http.MethodPut, "/api/v1/me/player-settings",
		`{"default_quality":"720p"}`, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("second PUT = %d; body=%s", rec.Code, rec.Body.String())
	}
	got := getPlayerSettings(t, srv, tok)
	want.DefaultQuality = "720p"
	if got != want {
		t.Fatalf("after second PUT = %+v, want %+v (speed/captions must persist)", got, want)
	}
}

// TestPlayerSettingsValidation: invalid speed/quality and a malformed body are
// 400 and nothing is written.
func TestPlayerSettingsValidation(t *testing.T) {
	srv := videoServer(t)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	cases := []struct {
		name string
		body string
	}{
		{"speed off the ladder", `{"default_speed":1.1}`},
		{"speed zero", `{"default_speed":0}`},
		{"speed too fast", `{"default_speed":5}`},
		{"quality no p", `{"default_quality":"720"}`},
		{"quality one digit", `{"default_quality":"7p"}`},
		{"quality garbage", `{"default_quality":"ultra"}`},
		{"malformed speed type", `{"default_speed":"fast"}`},
		{"malformed bool type", `{"autoplay_next":"yes"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/me/player-settings", tc.body, tok)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PUT %s = %d, want 400; body=%s", tc.body, rec.Code, rec.Body.String())
			}
		})
	}

	// After all the rejects, the stored settings are still the untouched defaults.
	got := getPlayerSettings(t, srv, tok)
	if got != (playerSettingsResponse{AutoplayNext: true, DefaultSpeed: 1, DefaultQuality: "auto"}) {
		t.Fatalf("settings mutated by rejected updates: %+v", got)
	}
}

// TestPlayerSettingsLadderAccepted: every rung of the shared speed ladder is
// accepted (the contract the frontend PLAYBACK_RATES relies on).
func TestPlayerSettingsLadderAccepted(t *testing.T) {
	srv := videoServer(t)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	for _, r := range []string{"0.25", "0.5", "0.75", "1", "1.25", "1.5", "1.75", "2", "2.5", "3", "3.5", "4"} {
		rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/me/player-settings",
			`{"default_speed":`+r+`}`, tok)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT default_speed=%s = %d, want 200; body=%s", r, rec.Code, rec.Body.String())
		}
	}
}

// TestPlayerSettingsPerUserIsolation: one user's settings never leak into
// another user's GET.
func TestPlayerSettingsPerUserIsolation(t *testing.T) {
	srv := videoServer(t)
	ada := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/me/player-settings",
		`{"autoplay_next":false,"default_speed":2,"theater_default":true}`, ada); rec.Code != http.StatusOK {
		t.Fatalf("ada PUT = %d; body=%s", rec.Code, rec.Body.String())
	}
	// Bob still sees the pristine defaults.
	bobGot := getPlayerSettings(t, srv, bob)
	if bobGot != (playerSettingsResponse{AutoplayNext: true, DefaultSpeed: 1, DefaultQuality: "auto"}) {
		t.Fatalf("bob's settings polluted by ada: %+v", bobGot)
	}
	// Ada sees her own.
	adaGot := getPlayerSettings(t, srv, ada)
	if !(!adaGot.AutoplayNext && adaGot.DefaultSpeed == 2 && adaGot.TheaterDefault) {
		t.Fatalf("ada's settings not persisted: %+v", adaGot)
	}
}

// TestPlayerSettingsRequireAuth: both routes are 401 without a token.
func TestPlayerSettingsRequireAuth(t *testing.T) {
	srv := videoServer(t)
	for _, m := range []struct {
		method, body string
	}{
		{http.MethodGet, ""},
		{http.MethodPut, `{"default_speed":1}`},
	} {
		rec := sendJSONAuth(srv, m.method, "/api/v1/me/player-settings", m.body, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without token = %d, want 401", m.method, rec.Code)
		}
	}
}

// TestPlayerSettingsEmptyPutIsNoop: an empty body is a valid no-op that returns
// the current effective object (documented merge semantics).
func TestPlayerSettingsEmptyPutIsNoop(t *testing.T) {
	srv := videoServer(t)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Seed a value, then send {} — it must survive.
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/me/player-settings", `{"default_speed":2}`, tok); rec.Code != http.StatusOK {
		t.Fatalf("seed PUT = %d; body=%s", rec.Code, rec.Body.String())
	}
	rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/me/player-settings", `{}`, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty PUT = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := getPlayerSettings(t, srv, tok); got.DefaultSpeed != 2 {
		t.Fatalf("empty PUT changed a value: %+v", got)
	}
}
