package httpapi

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/admin"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// memLockStore is a minimal in-memory auth.LoginLockStore (one scope is enough
// for the handler tests; threshold maths is pinned in internal/auth).
type memLockStore struct {
	count  map[uuid.UUID]int32
	locked map[uuid.UUID]bool
}

func (m *memLockStore) RecordLoginFailure(_ context.Context, a sqlcgen.RecordLoginFailureParams) (int32, error) {
	m.count[a.UserID]++
	return m.count[a.UserID], nil
}
func (m *memLockStore) SetLoginLock(_ context.Context, a sqlcgen.SetLoginLockParams) error {
	m.locked[a.UserID] = true
	return nil
}
func (m *memLockStore) CountActiveLoginLocks(_ context.Context, a sqlcgen.CountActiveLoginLocksParams) (int64, error) {
	if m.locked[a.UserID] {
		return 1, nil
	}
	return 0, nil
}
func (m *memLockStore) ClearLoginThrottleScope(context.Context, sqlcgen.ClearLoginThrottleScopeParams) error {
	return nil
}
func (m *memLockStore) ClearLoginThrottles(_ context.Context, id uuid.UUID) (int64, error) {
	n := int64(m.count[id])
	delete(m.count, id)
	delete(m.locked, id)
	return n, nil
}

func TestAdminClearsLoginLockout(t *testing.T) {
	repo := newAuthFakeRepo()
	store := &memLockStore{count: map[uuid.UUID]int32{}, locked: map[uuid.UUID]bool{}}
	logs := &bytes.Buffer{}
	issuer := auth.NewTokenIssuer(mfaTestSecret, "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour, auth.WithLoginLockStore(store))
	srv := New(testConfig(), nil, nil, WithAuthService(svc, 15*time.Minute),
		WithAdminService(admin.NewService(repo)),
		WithLogger(slog.New(slog.NewJSONHandler(logs, nil))))
	adminTok := registerTokens(t, srv, `{"username":"mona","email":"mona@example.test","password":"supersecret"}`).Token
	userTok := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`).Token
	adaID := userIDByName(t, adminUsers(t, srv, "", adminTok).Users, "ada")

	// Lock ada out: five wrong passwords, then even the right one is refused —
	// with the SAME status and body as a plain wrong password and an unknown account.
	bad := `{"identifier":"ada","password":"wrong-password"}`
	var wrongBody string
	for i := 0; i < 5; i++ {
		rec := postTo(srv, "/api/v1/auth/login", bad)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("bad login = %d", rec.Code)
		}
		wrongBody = noReqID(rec.Body.String())
	}
	locked := postTo(srv, "/api/v1/auth/login", `{"identifier":"ada","password":"supersecret"}`)
	unknown := postTo(srv, "/api/v1/auth/login", `{"identifier":"nobody-here","password":"supersecret"}`)
	if locked.Code != http.StatusUnauthorized || unknown.Code != http.StatusUnauthorized {
		t.Fatalf("locked=%d unknown=%d, want both 401", locked.Code, unknown.Code)
	}
	if noReqID(locked.Body.String()) != noReqID(unknown.Body.String()) || noReqID(locked.Body.String()) != wrongBody {
		t.Fatalf("bodies differ: locked=%q unknown=%q wrong=%q", locked.Body.String(), unknown.Body.String(), wrongBody)
	}
	if locked.Header().Get("Retry-After") != "" {
		t.Error("a lock must not advertise Retry-After")
	}

	path := "/api/v1/admin/users/" + adaID + "/login-lockout"
	for _, tc := range []struct {
		name, token string
		want        int
	}{{"anonymous", "", 401}, {"ordinary user", userTok, 403}} {
		if rec := sendJSONAuth(srv, http.MethodDelete, path, "", tc.token); rec.Code != tc.want {
			t.Errorf("%s clear = %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/users/"+uuid.NewString()+"/login-lockout", "", adminTok); rec.Code != http.StatusNotFound {
		t.Errorf("unknown user = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, path, "", adminTok); rec.Code != http.StatusNoContent {
		t.Fatalf("admin clear = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := postTo(srv, "/api/v1/auth/login", `{"identifier":"ada","password":"supersecret"}`); rec.Code != http.StatusOK {
		t.Fatalf("login after clear = %d, want 200", rec.Code)
	}
	ev := findAudit(auditEvents(t, logs), observability.ActionAdminLoginLockoutClear, observability.ResultSuccess)
	if ev == nil {
		t.Fatal("no admin.user.login_lockout_clear audit event")
	}
	if ev["resource_id"] != adaID {
		t.Errorf("audit resource_id = %v, want %s", ev["resource_id"], adaID)
	}
	if strings.Contains(logs.String(), "supersecret") {
		t.Error("a password reached the logs")
	}
}

var reqIDRe = regexp.MustCompile(`"request_id":"[^"]*"`)

// noReqID drops the per-request correlation id, the one field that legitimately differs.
func noReqID(b string) string { return reqIDRe.ReplaceAllString(b, "") }
