//go:build integration

// Integration coverage for cookie-mode refresh sessions against a live
// PostgreSQL (real users/sessions tables + sqlc queries). Requires migrations
// applied. Run with:
//
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration ./internal/httpapi/ -run TestCookieModeSessionsIntegration
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/store"
)

// TestCookieModeSessionsIntegration proves the full browser session lifecycle
// over the real database: register in cookie mode (session row persisted,
// hashed), refresh driven purely by the vidra_refresh cookie (rotation revokes
// the old row and re-sets the cookie), and cookie logout (revocation + clear).
func TestCookieModeSessionsIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// Registered before the row cleanup below so it runs after it (LIFO).
	t.Cleanup(st.Close)

	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(st.Queries(), issuer, time.Hour)
	srv := New(testConfig(), st, nil, WithAuthService(svc, 15*time.Minute))

	// Unique identity so reruns against a shared dev database never collide.
	suffix := uuid.New().String()[:8]
	body := fmt.Sprintf(`{"username":"ck-%s","email":"ck-%s@example.test","password":"supersecret","cookie_mode":true}`, suffix, suffix)

	reg := postTo(srv, "/api/v1/auth/register", body)
	if reg.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body=%s", reg.Code, reg.Body.String())
	}
	var created authResponse
	if err := json.Unmarshal(reg.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal register: %v", err)
	}
	userID, err := uuid.Parse(created.User.ID)
	if err != nil {
		t.Fatalf("parse user id: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanCancel()
		if _, err := st.Pool.Exec(cleanCtx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})
	if created.RefreshToken != "" {
		t.Error("cookie-mode register body leaked refresh_token")
	}
	first := refreshCookieFrom(reg)
	if first == nil || first.Value == "" || !first.HttpOnly {
		t.Fatalf("register did not set an httpOnly vidra_refresh cookie: %+v", first)
	}

	// Refresh with ONLY the cookie: rotation persists (old row revoked in the
	// sessions table) and the response re-sets the cookie with the new token.
	ref := postJSONCookies(srv, "/api/v1/auth/refresh", `{}`, first)
	if ref.Code != http.StatusOK {
		t.Fatalf("refresh-from-cookie status = %d, want 200; body=%s", ref.Code, ref.Body.String())
	}
	rotated := refreshCookieFrom(ref)
	if rotated == nil || rotated.Value == "" || rotated.Value == first.Value {
		t.Fatalf("cookie not rotated: old set, new=%+v", rotated)
	}

	// The pre-rotation cookie is dead against the real sessions table.
	if reuse := postJSONCookies(srv, "/api/v1/auth/refresh", `{}`, first); reuse.Code != http.StatusUnauthorized {
		t.Errorf("reused pre-rotation cookie status = %d, want 401", reuse.Code)
	}

	// Reuse detection revoked everything for the user; sign back in (cookie
	// mode) and prove cookie logout revokes the DB session and clears the cookie.
	login := postTo(srv, "/api/v1/auth/login",
		fmt.Sprintf(`{"email":"ck-%s@example.test","password":"supersecret","cookie_mode":true}`, suffix))
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", login.Code, login.Body.String())
	}
	sessionCookie := refreshCookieFrom(login)
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("login did not set the refresh cookie")
	}

	out := postJSONCookies(srv, "/api/v1/auth/logout", `{}`, sessionCookie)
	if out.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204; body=%s", out.Code, out.Body.String())
	}
	if cleared := refreshCookieFrom(out); cleared == nil || cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Errorf("logout did not clear the refresh cookie: %+v", cleared)
	}
	if rec := postJSONCookies(srv, "/api/v1/auth/refresh", `{}`, sessionCookie); rec.Code != http.StatusUnauthorized {
		t.Errorf("refresh after cookie logout = %d, want 401 (session must be revoked in the DB)", rec.Code)
	}
}
