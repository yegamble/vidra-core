package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// authFakeRepo is a tiny in-memory auth.Repository for handler tests.
type authFakeRepo struct {
	users    map[string]sqlcgen.User // keyed by lowercased email
	sessions map[uuid.UUID]*sqlcgen.GetSessionByRefreshHashRow
}

func newAuthFakeRepo() *authFakeRepo {
	return &authFakeRepo{
		users:    map[string]sqlcgen.User{},
		sessions: map[uuid.UUID]*sqlcgen.GetSessionByRefreshHashRow{},
	}
}

func (f *authFakeRepo) CountUsers(context.Context) (int64, error) { return int64(len(f.users)), nil }

func (f *authFakeRepo) CreateSession(_ context.Context, a sqlcgen.CreateSessionParams) (sqlcgen.CreateSessionRow, error) {
	id := uuid.New()
	f.sessions[id] = &sqlcgen.GetSessionByRefreshHashRow{
		ID: id, UserID: a.UserID, RefreshHash: a.RefreshHash,
		UserAgent: a.UserAgent, ExpiresAt: a.ExpiresAt, CreatedAt: time.Now(),
	}
	return sqlcgen.CreateSessionRow{ID: id, UserID: a.UserID, RefreshHash: a.RefreshHash, ExpiresAt: a.ExpiresAt}, nil
}

func (f *authFakeRepo) GetSessionByRefreshHash(_ context.Context, hash string) (sqlcgen.GetSessionByRefreshHashRow, error) {
	for _, s := range f.sessions {
		if s.RefreshHash == hash {
			return *s, nil
		}
	}
	return sqlcgen.GetSessionByRefreshHashRow{}, errors.New("not found")
}

func (f *authFakeRepo) RevokeSession(_ context.Context, id uuid.UUID) error {
	if s, ok := f.sessions[id]; ok {
		s.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}
	return nil
}

func (f *authFakeRepo) RevokeAllUserSessions(_ context.Context, userID uuid.UUID) error {
	for _, s := range f.sessions {
		if s.UserID == userID {
			s.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}
	}
	return nil
}

func (f *authFakeRepo) CreateUser(_ context.Context, a sqlcgen.CreateUserParams) (sqlcgen.User, error) {
	key := strings.ToLower(a.Email)
	if _, ok := f.users[key]; ok {
		return sqlcgen.User{}, &pgconn.PgError{Code: "23505"}
	}
	u := sqlcgen.User{
		ID: uuid.New(), Username: a.Username, Email: a.Email,
		PasswordHash: a.PasswordHash, Role: a.Role, IsActive: true, CreatedAt: time.Now(),
	}
	f.users[key] = u
	return u, nil
}

func (f *authFakeRepo) GetUserByEmail(_ context.Context, lowerEmail string) (sqlcgen.User, error) {
	u, ok := f.users[strings.ToLower(lowerEmail)]
	if !ok {
		return sqlcgen.User{}, errors.New("not found")
	}
	return u, nil
}

func (f *authFakeRepo) GetUserByID(_ context.Context, id uuid.UUID) (sqlcgen.User, error) {
	for _, u := range f.users {
		if u.ID == id {
			return u, nil
		}
	}
	return sqlcgen.User{}, errors.New("not found")
}

func authServer(t *testing.T) *Server {
	t.Helper()
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	svc := auth.NewService(repo, issuer, 720*time.Hour)
	return New(testConfig(), nil, nil, WithAuthService(svc, 15*time.Minute))
}

func postTo(srv *Server, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestRegisterEndpointCreatesAccount(t *testing.T) {
	srv := authServer(t)
	rec := postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var body authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Token == "" || body.TokenType != "Bearer" || body.ExpiresIn <= 0 {
		t.Errorf("unexpected auth response: %+v", body)
	}
	if body.User.Role != "admin" {
		t.Errorf("first user role = %q, want admin", body.User.Role)
	}
	// The password hash must never appear in the response.
	if strings.Contains(rec.Body.String(), "password_hash") {
		t.Error("response leaked password_hash")
	}
}

func TestRegisterEndpointValidationError(t *testing.T) {
	srv := authServer(t)
	rec := postTo(srv, "/api/v1/auth/register", `{"username":"a","email":"nope","password":"short"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	var body ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Code != "unprocessable_entity" || len(body.Error.Fields) == 0 {
		t.Errorf("expected field errors, got %+v", body.Error)
	}
}

func TestRegisterEndpointDuplicateConflict(t *testing.T) {
	srv := authServer(t)
	const body = `{"username":"ada","email":"ada@example.test","password":"supersecret"}`
	_ = postTo(srv, "/api/v1/auth/register", body)
	rec := postTo(srv, "/api/v1/auth/register", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if er.Error.Code != "conflict" {
		t.Errorf("code = %q, want conflict", er.Error.Code)
	}
}

// registerAndToken registers an account and returns its access token.
func registerAndToken(t *testing.T, srv *Server, body string) string {
	t.Helper()
	rec := postTo(srv, "/api/v1/auth/register", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var ar authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ar); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return ar.Token
}

func getWithAuth(srv *Server, path, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestMeRequiresAuth(t *testing.T) {
	srv := authServer(t)
	rec := getWithAuth(srv, "/api/v1/auth/me", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if er.Error.Code != "unauthorized" {
		t.Errorf("code = %q, want unauthorized", er.Error.Code)
	}
}

func TestMeRejectsBadToken(t *testing.T) {
	srv := authServer(t)
	rec := getWithAuth(srv, "/api/v1/auth/me", "not-a-real-token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMeReturnsCurrentUser(t *testing.T) {
	srv := authServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := getWithAuth(srv, "/api/v1/auth/me", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var u userView
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if u.Username != "ada" || u.Email != "ada@example.test" {
		t.Errorf("unexpected user: %+v", u)
	}
	if strings.Contains(rec.Body.String(), "password_hash") {
		t.Error("response leaked password_hash")
	}
}

func TestLoginEndpointSuccessAndFailure(t *testing.T) {
	srv := authServer(t)
	_ = postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	ok := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body=%s", ok.Code, ok.Body.String())
	}

	bad := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"wrong"}`)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("bad login status = %d, want 401", bad.Code)
	}
	var er ErrorResponse
	_ = json.Unmarshal(bad.Body.Bytes(), &er)
	if er.Error.Code != "unauthorized" {
		t.Errorf("code = %q, want unauthorized", er.Error.Code)
	}
}

// registerTokens registers an account and returns the full token pair.
func registerTokens(t *testing.T, srv *Server, body string) authResponse {
	t.Helper()
	rec := postTo(srv, "/api/v1/auth/register", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var ar authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ar); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return ar
}

func TestRegisterReturnsRefreshToken(t *testing.T) {
	ar := registerTokens(t, authServer(t), `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if ar.RefreshToken == "" {
		t.Error("register did not return a refresh_token")
	}
}

func TestRefreshEndpointRotates(t *testing.T) {
	srv := authServer(t)
	ar := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := postTo(srv, "/api/v1/auth/refresh", `{"refresh_token":"`+ar.RefreshToken+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var rotated authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rotated.RefreshToken == "" || rotated.RefreshToken == ar.RefreshToken {
		t.Errorf("refresh token not rotated: old=%q new=%q", ar.RefreshToken, rotated.RefreshToken)
	}

	// The old (now-rotated) token must be rejected.
	reuse := postTo(srv, "/api/v1/auth/refresh", `{"refresh_token":"`+ar.RefreshToken+`"}`)
	if reuse.Code != http.StatusUnauthorized {
		t.Fatalf("reuse status = %d, want 401", reuse.Code)
	}
}

func TestRefreshEndpointRejectsUnknown(t *testing.T) {
	srv := authServer(t)
	rec := postTo(srv, "/api/v1/auth/refresh", `{"refresh_token":"nope"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestRefreshEndpointValidation(t *testing.T) {
	srv := authServer(t)
	rec := postTo(srv, "/api/v1/auth/refresh", `{}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestLogoutEndpointRevokes(t *testing.T) {
	srv := authServer(t)
	ar := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	out := postTo(srv, "/api/v1/auth/logout", `{"refresh_token":"`+ar.RefreshToken+`"}`)
	if out.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", out.Code)
	}

	// After logout the refresh token can no longer be rotated.
	rec := postTo(srv, "/api/v1/auth/refresh", `{"refresh_token":"`+ar.RefreshToken+`"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh-after-logout status = %d, want 401", rec.Code)
	}
}
