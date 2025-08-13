package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/testutil"
	"athena/internal/usecase"
)

type authResp struct {
	User struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Email    string `json:"email"`
	} `json:"user"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func TestLogin_Integration_PersistsRefreshToken(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users", "refresh_tokens", "sessions")

	userRepo := repository.NewUserRepository(td.DB)
	authRepo := repository.NewAuthRepository(td.DB)
	s := NewServer(userRepo, authRepo, "test-secret", nil, 0)

	uname := "login_" + time.Now().Format("20060102150405")
	email := uname + "@example.com"
	pw := "secret-password-1"
	hash, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)

	u := &domain.User{ID: uuid.NewString(), Username: uname, Email: email, Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := userRepo.Create(context.Background(), u, string(hash)); err != nil {
		t.Fatalf("seed create failed: %v", err)
	}

	body := map[string]any{"email": email, "password": pw}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.Login(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rr.Code, rr.Body.String())
	}

	// Parse response envelope then payload
	var env integResp
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatalf("decode env: %v", err)
	}
	var payload authResp
	if err := json.Unmarshal(env.Data, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	if payload.RefreshToken == "" {
		t.Fatalf("expected refresh token in response")
	}

	// Ensure the refresh token exists in DB
	if _, err := authRepo.GetRefreshToken(context.Background(), payload.RefreshToken); err != nil {
		t.Fatalf("expected refresh token in DB: %v", err)
	}
}

func TestLogin_Integration_InvalidCredentials_NoUser(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users", "refresh_tokens")

	s := NewServer(repository.NewUserRepository(td.DB), repository.NewAuthRepository(td.DB), "test-secret", nil, 0)

	body := map[string]any{"email": "nouser@example.com", "password": "some-password"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.Login(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestLogin_Integration_InvalidCredentials_WrongPassword(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users", "refresh_tokens")

	userRepo := repository.NewUserRepository(td.DB)
	s := NewServer(userRepo, repository.NewAuthRepository(td.DB), "test-secret", nil, 0)

	// seed user with known password
	email := "wrongpw_" + time.Now().Format("150405") + "@example.com"
	pw := "correct-password"
	hash, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	u := &domain.User{ID: uuid.NewString(), Username: "wrongpw", Email: email, Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := userRepo.Create(context.Background(), u, string(hash)); err != nil {
		t.Fatalf("seed create failed: %v", err)
	}

	body := map[string]any{"email": email, "password": "incorrect"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.Login(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestLogin_Integration_MissingFields(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users")

	s := NewServer(repository.NewUserRepository(td.DB), repository.NewAuthRepository(td.DB), "test-secret", nil, 0)

	// missing password
	body := map[string]any{"email": "someone@example.com"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.Login(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestRefresh_Integration_RotatesToken(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users", "refresh_tokens")

	userRepo := repository.NewUserRepository(td.DB)
	authRepo := repository.NewAuthRepository(td.DB)
	s := NewServer(userRepo, authRepo, "test-secret", nil, 0)

	// Seed user
	email := "refresh_" + time.Now().Format("150405") + "@example.com"
	pw := "super-secret"
	hash, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	u := &domain.User{ID: uuid.NewString(), Username: "refresh_user", Email: email, Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := userRepo.Create(context.Background(), u, string(hash)); err != nil {
		t.Fatalf("seed create failed: %v", err)
	}

	// Login to get refresh token
	lb, _ := json.Marshal(map[string]any{"email": email, "password": pw})
	lreq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(lb))
	lreq.Header.Set("Content-Type", "application/json")
	lrr := httptest.NewRecorder()
	s.Login(lrr, lreq)
	if lrr.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", lrr.Code, lrr.Body.String())
	}
	var lenv integResp
	_ = json.NewDecoder(lrr.Body).Decode(&lenv)
	var lauth authResp
	_ = json.Unmarshal(lenv.Data, &lauth)

	// Refresh
	rb, _ := json.Marshal(map[string]any{"refresh_token": lauth.RefreshToken})
	rreq := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(rb))
	rreq.Header.Set("Content-Type", "application/json")
	rrr := httptest.NewRecorder()
	s.RefreshToken(rrr, rreq)
	if rrr.Code != http.StatusOK {
		t.Fatalf("refresh failed: %d %s", rrr.Code, rrr.Body.String())
	}

	var renv integResp
	_ = json.NewDecoder(rrr.Body).Decode(&renv)
	var rpayload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.Unmarshal(renv.Data, &rpayload)
	if rpayload.RefreshToken == "" || rpayload.AccessToken == "" {
		t.Fatalf("missing tokens after refresh")
	}
	// Old token should be revoked (not returned by GetRefreshToken)
	if _, err := authRepo.GetRefreshToken(context.Background(), lauth.RefreshToken); err == nil {
		t.Fatalf("expected old refresh token to be revoked")
	}
	// New token exists
	if _, err := authRepo.GetRefreshToken(context.Background(), rpayload.RefreshToken); err != nil {
		t.Fatalf("expected new refresh token in DB: %v", err)
	}
}

func TestRefresh_Integration_MissingToken(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users", "refresh_tokens")

	s := NewServer(repository.NewUserRepository(td.DB), repository.NewAuthRepository(td.DB), "test-secret", nil, 0)

	// missing refresh_token field
	b, _ := json.Marshal(map[string]any{})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.RefreshToken(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestRefresh_Integration_InvalidToken(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users", "refresh_tokens")

	s := NewServer(repository.NewUserRepository(td.DB), repository.NewAuthRepository(td.DB), "test-secret", nil, 0)

	b, _ := json.Marshal(map[string]any{"refresh_token": "does-not-exist"})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.RefreshToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestLogout_Integration_RevokesTokens(t *testing.T) {
	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users", "refresh_tokens", "sessions")

	userRepo := repository.NewUserRepository(td.DB)
	authRepo := repository.NewAuthRepository(td.DB)
	s := NewServer(userRepo, authRepo, "test-secret", nil, 0)

	// Use the middleware stubbed userID = "user123"
	uid := uuid.NewString()
	u := &domain.User{ID: uid, Username: "logout_user", Email: "logout@example.com", Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := userRepo.Create(context.Background(), u, "hash"); err != nil {
		t.Fatalf("seed create failed: %v", err)
	}

	// Seed two refresh tokens (use valid UUIDs for IDs)
	t1 := &usecase.RefreshToken{ID: uuid.NewString(), UserID: uid, Token: uuid.NewString(), ExpiresAt: time.Now().Add(24 * time.Hour), CreatedAt: time.Now()}
	t2 := &usecase.RefreshToken{ID: uuid.NewString(), UserID: uid, Token: uuid.NewString(), ExpiresAt: time.Now().Add(24 * time.Hour), CreatedAt: time.Now()}
	if err := authRepo.CreateRefreshToken(context.Background(), t1); err != nil {
		t.Fatalf("seed rt1: %v", err)
	}
	if err := authRepo.CreateRefreshToken(context.Background(), t2); err != nil {
		t.Fatalf("seed rt2: %v", err)
	}

	// Call logout with userID in context
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, uid))
	rr := httptest.NewRecorder()
	s.Logout(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("logout failed: %d %s", rr.Code, rr.Body.String())
	}

	// Both tokens should now be revoked (GetRefreshToken should not return them)
	if _, err := authRepo.GetRefreshToken(context.Background(), t1.Token); err == nil {
		t.Fatalf("expected tok1 revoked")
	}
	if _, err := authRepo.GetRefreshToken(context.Background(), t2.Token); err == nil {
		t.Fatalf("expected tok2 revoked")
	}
}
