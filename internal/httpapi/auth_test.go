package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
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
	resets   map[string]*sqlcgen.PasswordResetToken     // keyed by token hash
	verifs   map[string]*sqlcgen.EmailVerificationToken // keyed by token hash
}

func newAuthFakeRepo() *authFakeRepo {
	return &authFakeRepo{
		users:    map[string]sqlcgen.User{},
		sessions: map[uuid.UUID]*sqlcgen.GetSessionByRefreshHashRow{},
		resets:   map[string]*sqlcgen.PasswordResetToken{},
		verifs:   map[string]*sqlcgen.EmailVerificationToken{},
	}
}

func (f *authFakeRepo) CreateEmailVerificationToken(_ context.Context, a sqlcgen.CreateEmailVerificationTokenParams) (sqlcgen.EmailVerificationToken, error) {
	t := sqlcgen.EmailVerificationToken{
		ID: uuid.New(), UserID: a.UserID, TokenHash: a.TokenHash,
		ExpiresAt: a.ExpiresAt, CreatedAt: time.Now(),
	}
	f.verifs[a.TokenHash] = &t
	return t, nil
}

func (f *authFakeRepo) GetEmailVerificationToken(_ context.Context, hash string) (sqlcgen.EmailVerificationToken, error) {
	if t, ok := f.verifs[hash]; ok {
		return *t, nil
	}
	return sqlcgen.EmailVerificationToken{}, errors.New("not found")
}

func (f *authFakeRepo) MarkEmailVerificationTokenUsed(_ context.Context, id uuid.UUID) error {
	for _, t := range f.verifs {
		if t.ID == id {
			t.UsedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}
	}
	return nil
}

func (f *authFakeRepo) DeleteUnusedEmailVerificationTokens(_ context.Context, userID uuid.UUID) error {
	for h, t := range f.verifs {
		if t.UserID == userID && !t.UsedAt.Valid {
			delete(f.verifs, h)
		}
	}
	return nil
}

func (f *authFakeRepo) SetUserEmailVerified(_ context.Context, id uuid.UUID) error {
	for k, u := range f.users {
		if u.ID == id {
			u.EmailVerified = true
			u.UpdatedAt = time.Now()
			f.users[k] = u
			return nil
		}
	}
	return errors.New("not found")
}

func (f *authFakeRepo) DeactivateUser(_ context.Context, id uuid.UUID) error {
	for k, u := range f.users {
		if u.ID == id {
			u.IsActive = false
			u.UpdatedAt = time.Now()
			f.users[k] = u
			return nil
		}
	}
	return errors.New("not found")
}

func (f *authFakeRepo) CreatePasswordResetToken(_ context.Context, a sqlcgen.CreatePasswordResetTokenParams) (sqlcgen.PasswordResetToken, error) {
	t := sqlcgen.PasswordResetToken{
		ID: uuid.New(), UserID: a.UserID, TokenHash: a.TokenHash,
		ExpiresAt: a.ExpiresAt, CreatedAt: time.Now(),
	}
	f.resets[a.TokenHash] = &t
	return t, nil
}

func (f *authFakeRepo) GetPasswordResetToken(_ context.Context, hash string) (sqlcgen.PasswordResetToken, error) {
	if t, ok := f.resets[hash]; ok {
		return *t, nil
	}
	return sqlcgen.PasswordResetToken{}, errors.New("not found")
}

func (f *authFakeRepo) MarkPasswordResetTokenUsed(_ context.Context, id uuid.UUID) error {
	for _, t := range f.resets {
		if t.ID == id {
			t.UsedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}
	}
	return nil
}

func (f *authFakeRepo) DeleteUnusedPasswordResetTokens(_ context.Context, userID uuid.UUID) error {
	for h, t := range f.resets {
		if t.UserID == userID && !t.UsedAt.Valid {
			delete(f.resets, h)
		}
	}
	return nil
}

func (f *authFakeRepo) UpdateUserPassword(_ context.Context, a sqlcgen.UpdateUserPasswordParams) error {
	for k, u := range f.users {
		if u.ID == a.ID {
			u.PasswordHash = a.PasswordHash
			u.UpdatedAt = time.Now()
			f.users[k] = u
			return nil
		}
	}
	return errors.New("not found")
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

func (f *authFakeRepo) UpdateUserProfile(_ context.Context, a sqlcgen.UpdateUserProfileParams) (sqlcgen.User, error) {
	for k, u := range f.users {
		if u.ID == a.ID {
			if a.DisplayName != nil {
				u.DisplayName = *a.DisplayName
			}
			if a.Bio != nil {
				u.Bio = *a.Bio
			}
			u.UpdatedAt = time.Now()
			f.users[k] = u
			return u, nil
		}
	}
	return sqlcgen.User{}, errors.New("not found")
}

// ListUsers + AdminUpdateUser let authFakeRepo also satisfy admin.Repository.
func (f *authFakeRepo) ListUsers(_ context.Context, a sqlcgen.ListUsersParams) ([]sqlcgen.User, error) {
	var out []sqlcgen.User
	q := strings.ToLower(a.Query)
	for _, u := range f.users {
		if q == "" || strings.Contains(strings.ToLower(u.Username), q) || strings.Contains(strings.ToLower(u.Email), q) {
			out = append(out, u)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	lo := int(a.ResultOffset)
	if lo > len(out) {
		lo = len(out)
	}
	hi := lo + int(a.ResultLimit)
	if hi > len(out) {
		hi = len(out)
	}
	return out[lo:hi], nil
}

func (f *authFakeRepo) AdminUpdateUser(_ context.Context, a sqlcgen.AdminUpdateUserParams) (sqlcgen.User, error) {
	for k, u := range f.users {
		if u.ID == a.ID {
			if a.Role != nil {
				u.Role = *a.Role
			}
			if a.IsActive != nil {
				u.IsActive = *a.IsActive
			}
			u.UpdatedAt = time.Now()
			f.users[k] = u
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

func postWithAuth(srv *Server, path, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestRequireRole exercises the role gate via a test-only route (no admin route
// exists in the public surface yet; P9 admin endpoints will mount this).
func TestRequireRole(t *testing.T) {
	srv := authServer(t)
	srv.echo.GET("/api/v1/_test/admin-only", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	}, srv.requireAuth, srv.requireRole("admin"))

	adminTok := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`).Token
	userTok := registerTokens(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`).Token

	if rec := getWithAuth(srv, "/api/v1/_test/admin-only", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d, want 401", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/_test/admin-only", userTok); rec.Code != http.StatusForbidden {
		t.Errorf("user token = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/_test/admin-only", adminTok); rec.Code != http.StatusOK {
		t.Errorf("admin token = %d, want 200", rec.Code)
	}
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

func TestUpdateMeProfile(t *testing.T) {
	srv := authServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Partial update: set display_name and bio.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"display_name":"Ada L.","bio":"hi"}`, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var u userView
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if u.DisplayName != "Ada L." || u.Bio != "hi" {
		t.Errorf("unexpected profile: %+v", u)
	}

	// GET /me reflects the change after a fresh read.
	me := getWithAuth(srv, "/api/v1/auth/me", token)
	var got userView
	_ = json.Unmarshal(me.Body.Bytes(), &got)
	if got.DisplayName != "Ada L." || got.Bio != "hi" {
		t.Errorf("me did not reflect update: %+v", got)
	}
}

func TestUpdateMeValidationAndAuth(t *testing.T) {
	srv := authServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	if empty := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{}`, token); empty.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty patch = %d, want 422", empty.Code)
	}
	if anon := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"bio":"x"}`, ""); anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon patch = %d, want 401", anon.Code)
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

func TestLogoutAllRequiresAuth(t *testing.T) {
	srv := authServer(t)
	rec := postTo(srv, "/api/v1/auth/logout-all", `{}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestLogoutAllRevokesEverySession(t *testing.T) {
	srv := authServer(t)
	first := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	// A second login creates a second session for the same account.
	loginRec := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`)
	var second authResponse
	if err := json.Unmarshal(loginRec.Body.Bytes(), &second); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	out := postWithAuth(srv, "/api/v1/auth/logout-all", first.Token)
	if out.Code != http.StatusNoContent {
		t.Fatalf("logout-all status = %d, want 204; body=%s", out.Code, out.Body.String())
	}

	for name, tok := range map[string]string{"first": first.RefreshToken, "second": second.RefreshToken} {
		rec := postTo(srv, "/api/v1/auth/refresh", `{"refresh_token":"`+tok+`"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s refresh after logout-all = %d, want 401", name, rec.Code)
		}
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

// captureResetMailer records the delivered token (reset or verification) so the
// handler tests can drive the confirm step.
type captureResetMailer struct {
	calls int
	token string
}

func (m *captureResetMailer) SendPasswordReset(_ context.Context, _, token string) error {
	m.calls++
	m.token = token
	return nil
}

func (m *captureResetMailer) SendEmailVerification(_ context.Context, _, token string) error {
	m.calls++
	m.token = token
	return nil
}

func authServerWithMailer(t *testing.T) (*Server, *captureResetMailer) {
	t.Helper()
	repo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	mailer := &captureResetMailer{}
	svc := auth.NewService(repo, issuer, 720*time.Hour, auth.WithMailer(mailer))
	return New(testConfig(), nil, nil, WithAuthService(svc, 15*time.Minute)), mailer
}

func TestPasswordResetFlow(t *testing.T) {
	srv, mailer := authServerWithMailer(t)
	_ = postTo(srv, "/api/v1/auth/register", `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	rec := postTo(srv, "/api/v1/auth/password-reset", `{"email":"ada@example.test"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("request status = %d, want 202", rec.Code)
	}
	if mailer.token == "" {
		t.Fatal("expected a reset token to be delivered")
	}

	rec = postTo(srv, "/api/v1/auth/password-reset/confirm", `{"token":"`+mailer.token+`","password":"brand-new-pass"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("confirm status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	// The new password logs in; the old one is rejected.
	if ok := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"brand-new-pass"}`); ok.Code != http.StatusOK {
		t.Errorf("login with new password = %d, want 200", ok.Code)
	}
	if bad := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`); bad.Code != http.StatusUnauthorized {
		t.Errorf("login with old password = %d, want 401", bad.Code)
	}
}

func TestPasswordResetRequestIsEnumerationSafe(t *testing.T) {
	srv, mailer := authServerWithMailer(t)
	rec := postTo(srv, "/api/v1/auth/password-reset", `{"email":"nobody@example.test"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 even for an unknown email", rec.Code)
	}
	if mailer.calls != 0 {
		t.Errorf("mailer called %d times for an unknown email, want 0", mailer.calls)
	}
}

func TestPasswordResetRequestValidatesEmail(t *testing.T) {
	srv, _ := authServerWithMailer(t)
	rec := postTo(srv, "/api/v1/auth/password-reset", `{"email":"not-an-email"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestPasswordResetConfirmRejectsBadToken(t *testing.T) {
	srv, _ := authServerWithMailer(t)
	rec := postTo(srv, "/api/v1/auth/password-reset/confirm", `{"token":"not-a-real-token","password":"brand-new-pass"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPasswordResetConfirmValidatesPassword(t *testing.T) {
	srv, _ := authServerWithMailer(t)
	rec := postTo(srv, "/api/v1/auth/password-reset/confirm", `{"token":"x","password":"short"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestEmailVerificationFlow(t *testing.T) {
	srv, mailer := authServerWithMailer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// A fresh account is not yet verified.
	var before userView
	if err := json.Unmarshal(getWithAuth(srv, "/api/v1/auth/me", reg.Token).Body.Bytes(), &before); err != nil {
		t.Fatalf("unmarshal me: %v", err)
	}
	if before.EmailVerified {
		t.Fatal("a fresh account should not be email-verified")
	}

	// Request verification (authed) → 202, token delivered.
	rec := postWithAuth(srv, "/api/v1/auth/verify-email", reg.Token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("request status = %d, want 202", rec.Code)
	}
	if mailer.token == "" {
		t.Fatal("expected a verification token to be delivered")
	}

	// Confirm (public, with the token) → 204.
	rec = postTo(srv, "/api/v1/auth/verify-email/confirm", `{"token":"`+mailer.token+`"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("confirm status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	// /me now reflects the verified state.
	var after userView
	if err := json.Unmarshal(getWithAuth(srv, "/api/v1/auth/me", reg.Token).Body.Bytes(), &after); err != nil {
		t.Fatalf("unmarshal me: %v", err)
	}
	if !after.EmailVerified {
		t.Error("email_verified should be true after confirm")
	}
}

func TestEmailVerificationRequestRequiresAuth(t *testing.T) {
	srv, _ := authServerWithMailer(t)
	rec := postTo(srv, "/api/v1/auth/verify-email", `{}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 without a token", rec.Code)
	}
}

func TestEmailVerificationConfirmRejectsBadToken(t *testing.T) {
	srv, _ := authServerWithMailer(t)
	rec := postTo(srv, "/api/v1/auth/verify-email/confirm", `{"token":"not-a-real-token"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestEmailVerificationConfirmValidatesToken(t *testing.T) {
	srv, _ := authServerWithMailer(t)
	rec := postTo(srv, "/api/v1/auth/verify-email/confirm", `{}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestDeactivateAccountFlow(t *testing.T) {
	srv := authServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Wrong password is rejected and leaves the account active.
	bad := sendJSONAuth(srv, http.MethodPost, "/api/v1/auth/me/deactivate", `{"password":"wrong"}`, reg.Token)
	if bad.Code != http.StatusForbidden {
		t.Fatalf("wrong-password status = %d, want 403", bad.Code)
	}
	if me := getWithAuth(srv, "/api/v1/auth/me", reg.Token); me.Code != http.StatusOK {
		t.Fatalf("account should still be usable after a failed deactivate: /me = %d", me.Code)
	}

	// Correct password deactivates → 204.
	ok := sendJSONAuth(srv, http.MethodPost, "/api/v1/auth/me/deactivate", `{"password":"supersecret"}`, reg.Token)
	if ok.Code != http.StatusNoContent {
		t.Fatalf("deactivate status = %d, want 204; body=%s", ok.Code, ok.Body.String())
	}

	// Login is now refused (account disabled), and the access token stops resolving.
	if login := postTo(srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`); login.Code != http.StatusForbidden {
		t.Errorf("login after deactivate = %d, want 403", login.Code)
	}
	if me := getWithAuth(srv, "/api/v1/auth/me", reg.Token); me.Code != http.StatusUnauthorized {
		t.Errorf("/me after deactivate = %d, want 401", me.Code)
	}
}

func TestDeactivateAccountRequiresAuth(t *testing.T) {
	srv := authServer(t)
	rec := postTo(srv, "/api/v1/auth/me/deactivate", `{"password":"supersecret"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 without a token", rec.Code)
	}
}

func TestDeactivateAccountValidatesPassword(t *testing.T) {
	srv := authServer(t)
	reg := registerTokens(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/auth/me/deactivate", `{}`, reg.Token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}
