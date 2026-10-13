package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// memDeviceStore is a minimal in-memory auth.DeviceStore.
type memDeviceStore struct {
	rows   []sqlcgen.ListKnownDevicesRow
	owner  map[uuid.UUID]uuid.UUID
	optOut map[uuid.UUID]bool
	mailed map[uuid.UUID]bool
}

func (m *memDeviceStore) CountUserKnownDevices(_ context.Context, u uuid.UUID) (int64, error) {
	var n int64
	for _, r := range m.rows {
		if m.owner[r.ID] == u {
			n++
		}
	}
	return n, nil
}
func (m *memDeviceStore) TouchKnownDevice(_ context.Context, a sqlcgen.TouchKnownDeviceParams) (bool, error) {
	for _, r := range m.rows {
		if m.owner[r.ID] == a.UserID && r.DeviceHash == a.DeviceHash {
			return false, nil
		}
	}
	id := uuid.New()
	m.rows = append(m.rows, sqlcgen.ListKnownDevicesRow{ID: id, DeviceHash: a.DeviceHash, Label: a.Label})
	m.owner[id] = a.UserID
	return true, nil
}
func (m *memDeviceStore) HasKnownDevice(_ context.Context, a sqlcgen.HasKnownDeviceParams) (bool, error) {
	for _, r := range m.rows {
		if m.owner[r.ID] == a.UserID && r.DeviceHash == a.DeviceHash {
			return true, nil
		}
	}
	return false, nil
}
func (m *memDeviceStore) PruneKnownDevices(context.Context, sqlcgen.PruneKnownDevicesParams) (int64, error) {
	return 0, nil
}
func (m *memDeviceStore) ListKnownDevices(_ context.Context, u uuid.UUID) ([]sqlcgen.ListKnownDevicesRow, error) {
	var out []sqlcgen.ListKnownDevicesRow
	for _, r := range m.rows {
		if m.owner[r.ID] == u {
			out = append(out, r)
		}
	}
	return out, nil
}
func (m *memDeviceStore) DeleteKnownDevice(_ context.Context, a sqlcgen.DeleteKnownDeviceParams) (int64, error) {
	for i, r := range m.rows {
		if r.ID == a.ID && m.owner[r.ID] == a.UserID {
			m.rows = append(m.rows[:i], m.rows[i+1:]...)
			return 1, nil
		}
	}
	return 0, nil
}
func (m *memDeviceStore) GetSecurityPrefs(_ context.Context, u uuid.UUID) (bool, error) {
	if v, ok := m.optOut[u]; ok {
		return !v, nil
	}
	return false, pgx.ErrNoRows
}
func (m *memDeviceStore) SetNewDeviceEmailPref(_ context.Context, a sqlcgen.SetNewDeviceEmailPrefParams) error {
	m.optOut[a.UserID] = !a.NewDeviceEmail
	return nil
}
func (m *memDeviceStore) ClaimNewDeviceEmailSlot(_ context.Context, a sqlcgen.ClaimNewDeviceEmailSlotParams) (bool, error) {
	if m.mailed[a.UserID] {
		return false, nil
	}
	m.mailed[a.UserID] = true
	return true, nil
}

const (
	devUAChrome  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/130.0 Safari/537.36"
	devUAFirefox = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:130.0) Gecko/20100101 Firefox/130.0"
)

func loginAs(srv *Server, ua, cookie string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"identifier":"ada","password":"supersecret"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("User-Agent", ua)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: deviceCookieName, Value: cookie})
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func deviceCookieOf(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == deviceCookieName {
			return c.Value
		}
	}
	return ""
}

func TestNewDeviceEmailListForgetAndOptOut(t *testing.T) {
	repo := newAuthFakeRepo()
	store := &memDeviceStore{owner: map[uuid.UUID]uuid.UUID{}, optOut: map[uuid.UUID]bool{}, mailed: map[uuid.UUID]bool{}}
	mailer := auth.NewCaptureMailer()
	issuer := auth.NewTokenIssuer(mfaTestSecret, "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour, auth.WithDeviceStore(store), auth.WithMailer(mailer), auth.WithAsyncRunner(func(f func()) { f() }))
	logs := &bytes.Buffer{}
	srv := New(testConfig(), nil, nil, WithAuthService(svc, 15*time.Minute), WithLogger(slog.New(slog.NewJSONHandler(logs, nil))))
	registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// First device: records silently and hands the browser a device cookie.
	first := loginAs(srv, devUAChrome, "")
	cookieA := deviceCookieOf(first)
	if first.Code != 200 || cookieA == "" {
		t.Fatalf("first login = %d, device cookie %q", first.Code, cookieA)
	}
	if n := len(mailer.NewDeviceNotices()); n != 0 {
		t.Fatalf("first device mailed %d times", n)
	}
	// Same device again: no mail. A different browser: exactly one mail.
	loginAs(srv, devUAChrome, cookieA)
	second := loginAs(srv, devUAFirefox, "")
	if n := len(mailer.NewDeviceNotices()); n != 1 {
		t.Fatalf("new device mails = %d, want 1", n)
	}
	loginAs(srv, devUAFirefox, deviceCookieOf(second))
	if n := len(mailer.NewDeviceNotices()); n != 1 {
		t.Fatalf("repeat sign-in re-mailed: %d", n)
	}

	// The list marks the current device; the cookie is httpOnly.
	tok := loginToken(t, srv, "ada@example.test", "supersecret")
	for _, c := range first.Result().Cookies() {
		if c.Name == deviceCookieName && (!c.HttpOnly || c.Path != "/api/v1/auth") {
			t.Errorf("device cookie must be httpOnly and scoped to /api/v1/auth: %+v", c)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/devices", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+tok)
	req.Header.Set("User-Agent", devUAChrome)
	req.AddCookie(&http.Cookie{Name: deviceCookieName, Value: cookieA})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var list devicesResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Devices) < 2 || !list.NewDeviceEmail {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	var current, other string
	for _, d := range list.Devices {
		if d.Current {
			current = d.Label
		} else if strings.HasPrefix(d.Label, "Firefox") {
			other = d.ID
		}
	}
	if current != "Chrome on macOS" || other == "" {
		t.Fatalf("current=%q other=%q in %+v", current, other, list.Devices)
	}

	// Another account cannot forget ada's device.
	bobTok := registerTokens(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`).Token
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/auth/devices/"+other, "", bobTok); rec.Code != http.StatusNotFound {
		t.Fatalf("another user's forget = %d, want 404", rec.Code)
	}
	// Forget: 204, then 404 (also for a stranger's / random id).
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/auth/devices/"+other, "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("forget = %d", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/auth/devices/"+other, "", tok); rec.Code != http.StatusNotFound {
		t.Fatalf("forget twice = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/devices", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list = %d", rec.Code)
	}

	// Opt-out round trip.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/security-prefs", `{}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty prefs = %d, want 422", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/security-prefs", `{"new_device_email":false}`, tok); rec.Code != http.StatusOK {
		t.Fatalf("opt-out = %d", rec.Code)
	}
	if findAudit(auditEvents(t, logs), observability.ActionSecurityPrefsUpdate, observability.ResultSuccess) == nil {
		t.Fatal("turning the alerts off must be audited")
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/devices", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+tok)
	srv.Handler().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"new_device_email":false`) {
		t.Fatalf("opt-out not reflected: %s", rec.Body.String())
	}
}
