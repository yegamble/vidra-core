//go:build !integration

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/usecase"
)

// ---------------------------------------------------------------------------
// Mock: AuthRepository
// ---------------------------------------------------------------------------

type mockAuthRepo struct {
	refreshTokens map[string]*usecase.RefreshToken
	sessions      map[string]string // sessionID -> userID

	// Error injection
	createRefreshErr error
	getRefreshErr    error
	createSessionErr error
}

var _ usecase.AuthRepository = (*mockAuthRepo)(nil)

func newMockAuthRepo() *mockAuthRepo {
	return &mockAuthRepo{
		refreshTokens: make(map[string]*usecase.RefreshToken),
		sessions:      make(map[string]string),
	}
}

func (m *mockAuthRepo) CreateRefreshToken(_ context.Context, token *usecase.RefreshToken) error {
	if m.createRefreshErr != nil {
		return m.createRefreshErr
	}
	m.refreshTokens[token.Token] = token
	return nil
}

func (m *mockAuthRepo) GetRefreshToken(_ context.Context, token string) (*usecase.RefreshToken, error) {
	if m.getRefreshErr != nil {
		return nil, m.getRefreshErr
	}
	if t, ok := m.refreshTokens[token]; ok {
		if t.RevokedAt != nil {
			return nil, errors.New("token revoked")
		}
		return t, nil
	}
	return nil, errors.New("refresh token not found")
}

func (m *mockAuthRepo) RevokeRefreshToken(_ context.Context, token string) error {
	if t, ok := m.refreshTokens[token]; ok {
		now := time.Now()
		t.RevokedAt = &now
	}
	return nil
}

func (m *mockAuthRepo) RevokeAllUserTokens(_ context.Context, userID string) error {
	now := time.Now()
	for _, t := range m.refreshTokens {
		if t.UserID == userID {
			t.RevokedAt = &now
		}
	}
	return nil
}

func (m *mockAuthRepo) CleanExpiredTokens(_ context.Context) error { return nil }

func (m *mockAuthRepo) CreateSession(_ context.Context, sessionID, userID string, _ time.Time) error {
	if m.createSessionErr != nil {
		return m.createSessionErr
	}
	m.sessions[sessionID] = userID
	return nil
}

func (m *mockAuthRepo) GetSession(_ context.Context, sessionID string) (string, error) {
	if uid, ok := m.sessions[sessionID]; ok {
		return uid, nil
	}
	return "", errors.New("session not found")
}

func (m *mockAuthRepo) DeleteSession(_ context.Context, sessionID string) error {
	delete(m.sessions, sessionID)
	return nil
}

func (m *mockAuthRepo) DeleteAllUserSessions(_ context.Context, userID string) error {
	for k, v := range m.sessions {
		if v == userID {
			delete(m.sessions, k)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Mock: OAuthRepository
// ---------------------------------------------------------------------------

type mockOAuthRepo struct {
	clients      map[string]*usecase.OAuthClient
	authCodes    map[string]*usecase.OAuthAuthorizationCode
	accessTokens map[string]*usecase.OAuthAccessToken

	// Error injection
	createClientErr error
	deleteClientErr error
	updateSecretErr error
	listClientsErr  error
}

var _ usecase.OAuthRepository = (*mockOAuthRepo)(nil)

func newMockOAuthRepo() *mockOAuthRepo {
	return &mockOAuthRepo{
		clients:      make(map[string]*usecase.OAuthClient),
		authCodes:    make(map[string]*usecase.OAuthAuthorizationCode),
		accessTokens: make(map[string]*usecase.OAuthAccessToken),
	}
}

func (m *mockOAuthRepo) CreateClient(_ context.Context, c *usecase.OAuthClient) error {
	if m.createClientErr != nil {
		return m.createClientErr
	}
	m.clients[c.ClientID] = c
	return nil
}

func (m *mockOAuthRepo) GetClientByClientID(_ context.Context, clientID string) (*usecase.OAuthClient, error) {
	if c, ok := m.clients[clientID]; ok {
		return c, nil
	}
	return nil, errors.New("client not found")
}

func (m *mockOAuthRepo) ListClients(_ context.Context) ([]*usecase.OAuthClient, error) {
	if m.listClientsErr != nil {
		return nil, m.listClientsErr
	}
	out := make([]*usecase.OAuthClient, 0, len(m.clients))
	for _, c := range m.clients {
		out = append(out, c)
	}
	return out, nil
}

func (m *mockOAuthRepo) UpdateClientSecret(_ context.Context, clientID string, secretHash *string, isConfidential bool) error {
	if m.updateSecretErr != nil {
		return m.updateSecretErr
	}
	if c, ok := m.clients[clientID]; ok {
		c.ClientSecretHash = secretHash
		c.IsConfidential = isConfidential
		return nil
	}
	return errors.New("client not found")
}

func (m *mockOAuthRepo) DeleteClient(_ context.Context, clientID string) error {
	if m.deleteClientErr != nil {
		return m.deleteClientErr
	}
	delete(m.clients, clientID)
	return nil
}

func (m *mockOAuthRepo) CreateAuthorizationCode(_ context.Context, code *usecase.OAuthAuthorizationCode) error {
	m.authCodes[code.Code] = code
	return nil
}

func (m *mockOAuthRepo) GetAuthorizationCode(_ context.Context, code string) (*usecase.OAuthAuthorizationCode, error) {
	if ac, ok := m.authCodes[code]; ok {
		return ac, nil
	}
	return nil, errors.New("authorization code not found")
}

func (m *mockOAuthRepo) MarkCodeAsUsed(_ context.Context, code string) error {
	if ac, ok := m.authCodes[code]; ok {
		now := time.Now()
		ac.UsedAt = &now
		return nil
	}
	return errors.New("code not found")
}

func (m *mockOAuthRepo) DeleteExpiredCodes(_ context.Context) error { return nil }

func (m *mockOAuthRepo) CreateAccessToken(_ context.Context, token *usecase.OAuthAccessToken) error {
	m.accessTokens[token.TokenHash] = token
	return nil
}

func (m *mockOAuthRepo) GetAccessToken(_ context.Context, tokenHash string) (*usecase.OAuthAccessToken, error) {
	if t, ok := m.accessTokens[tokenHash]; ok {
		return t, nil
	}
	return nil, errors.New("access token not found")
}

func (m *mockOAuthRepo) RevokeAccessToken(_ context.Context, tokenHash string) error {
	if t, ok := m.accessTokens[tokenHash]; ok {
		now := time.Now()
		t.RevokedAt = &now
		return nil
	}
	return errors.New("access token not found")
}

func (m *mockOAuthRepo) ListUserTokens(_ context.Context, userID string) ([]*usecase.OAuthAccessToken, error) {
	var out []*usecase.OAuthAccessToken
	for _, t := range m.accessTokens {
		if t.UserID == userID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *mockOAuthRepo) DeleteExpiredTokens(_ context.Context) error { return nil }

// ---------------------------------------------------------------------------
// Mock: TwoFAService (wrapping a mockable interface)
// ---------------------------------------------------------------------------

type mockTwoFAUserRepo struct {
	mockUserRepo
	passwords map[string]string // userID -> bcrypt hash
}

func newMockTwoFAUserRepo() *mockTwoFAUserRepo {
	return &mockTwoFAUserRepo{
		mockUserRepo: mockUserRepo{users: make(map[string]*domain.User)},
		passwords:    make(map[string]string),
	}
}

func (m *mockTwoFAUserRepo) GetPasswordHash(_ context.Context, userID string) (string, error) {
	if h, ok := m.passwords[userID]; ok {
		return h, nil
	}
	return "", errors.New("no hash")
}

// mockBackupCodeRepo implements usecase.TwoFABackupCodeRepository
type mockBackupCodeRepo struct {
	codes map[string][]*domain.TwoFABackupCode // userID -> backup codes
}

func newMockBackupCodeRepo() *mockBackupCodeRepo {
	return &mockBackupCodeRepo{codes: make(map[string][]*domain.TwoFABackupCode)}
}

func (m *mockBackupCodeRepo) Create(_ context.Context, code *domain.TwoFABackupCode) error {
	copyCode := *code
	if copyCode.ID == "" {
		copyCode.ID = uuid.NewString()
	}
	m.codes[copyCode.UserID] = append(m.codes[copyCode.UserID], &copyCode)
	return nil
}

func (m *mockBackupCodeRepo) GetUnusedForUser(_ context.Context, userID string) ([]*domain.TwoFABackupCode, error) {
	entries := m.codes[userID]
	out := make([]*domain.TwoFABackupCode, 0, len(entries))
	for _, code := range entries {
		if !code.UsedAt.Valid {
			copyCode := *code
			out = append(out, &copyCode)
		}
	}
	return out, nil
}

func (m *mockBackupCodeRepo) MarkAsUsed(_ context.Context, codeID string) error {
	for _, entries := range m.codes {
		for _, code := range entries {
			if code.ID == codeID {
				code.UsedAt.Valid = true
				code.UsedAt.Time = time.Now()
				return nil
			}
		}
	}
	return errors.New("backup code not found")
}

func (m *mockBackupCodeRepo) DeleteAllForUser(_ context.Context, userID string) error {
	delete(m.codes, userID)
	return nil
}

func (m *mockBackupCodeRepo) StoreBackupCodes(_ context.Context, userID string, codes []string) error {
	m.codes[userID] = make([]*domain.TwoFABackupCode, 0, len(codes))
	for _, c := range codes {
		hash, err := bcrypt.GenerateFromPassword([]byte(c), bcrypt.MinCost)
		if err != nil {
			return err
		}
		m.codes[userID] = append(m.codes[userID], &domain.TwoFABackupCode{
			ID:        uuid.NewString(),
			UserID:    userID,
			CodeHash:  string(hash),
			CreatedAt: time.Now(),
		})
	}
	return nil
}

func (m *mockBackupCodeRepo) UseBackupCode(_ context.Context, userID, code string) error {
	entries, ok := m.codes[userID]
	if !ok {
		return errors.New("no codes")
	}
	for _, entry := range entries {
		if entry.UsedAt.Valid {
			continue
		}
		if bcrypt.CompareHashAndPassword([]byte(entry.CodeHash), []byte(code)) == nil {
			entry.UsedAt.Valid = true
			entry.UsedAt.Time = time.Now()
			return nil
		}
	}
	return errors.New("invalid backup code")
}

func (m *mockBackupCodeRepo) GetRemainingCount(_ context.Context, userID string) (int, error) {
	count := 0
	for _, code := range m.codes[userID] {
		if !code.UsedAt.Valid {
			count++
		}
	}
	return count, nil
}

func (m *mockBackupCodeRepo) DeleteAll(_ context.Context, userID string) error {
	delete(m.codes, userID)
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestAuthHandlers creates an AuthHandlers instance with configurable mocks.
func newTestAuthHandlers(
	userRepo usecase.UserRepository,
	authRepo usecase.AuthRepository,
	oauthRepo usecase.OAuthRepository,
) *AuthHandlers {
	return &AuthHandlers{
		userRepo:  userRepo,
		authRepo:  authRepo,
		oauthRepo: oauthRepo,
		jwtSecret: "unit-test-secret",
	}
}

// mockUserRepoWithPasswords extends mockUserRepo with password hash storage.
type mockUserRepoWithPasswords struct {
	mockUserRepo
	passwords map[string]string
}

func newMockUserRepoWithPasswords() *mockUserRepoWithPasswords {
	return &mockUserRepoWithPasswords{
		mockUserRepo: mockUserRepo{users: make(map[string]*domain.User)},
		passwords:    make(map[string]string),
	}
}

func (m *mockUserRepoWithPasswords) Create(ctx context.Context, user *domain.User, passwordHash string) error {
	if err := m.mockUserRepo.Create(ctx, user, passwordHash); err != nil {
		return err
	}
	m.passwords[user.ID] = passwordHash
	return nil
}

func (m *mockUserRepoWithPasswords) GetPasswordHash(_ context.Context, userID string) (string, error) {
	if h, ok := m.passwords[userID]; ok {
		return h, nil
	}
	return "", errors.New("no hash")
}

// basicAuth encodes client_id:client_secret as a Basic Authorization header.
func basicAuth(clientID, clientSecret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(clientID+":"+clientSecret))
}

// oauthFormRequest creates a POST request with form-encoded body.
func oauthFormRequest(values url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// ---------------------------------------------------------------------------
// Tests: generateJWTWithRole / generateJWTWithRoleAndScope
// ---------------------------------------------------------------------------

func TestUnit_GenerateJWTWithRole_WithRole(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	tokenStr := h.generateJWTWithRole("user-1", "admin", 15*time.Minute)
	if tokenStr == "" {
		t.Fatal("expected non-empty token")
	}

	parsed, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		return []byte("unit-test-secret"), nil
	})
	if err != nil {
		t.Fatalf("failed to parse JWT: %v", err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("claims type assertion failed")
	}
	if claims["sub"] != "user-1" {
		t.Fatalf("expected sub=user-1, got %v", claims["sub"])
	}
	if claims["role"] != "admin" {
		t.Fatalf("expected role=admin, got %v", claims["role"])
	}
}

func TestUnit_GenerateJWTWithRole_EmptyRole(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	tokenStr := h.generateJWTWithRole("user-2", "", 15*time.Minute)
	if tokenStr == "" {
		t.Fatal("expected non-empty token")
	}

	parsed, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		return []byte("unit-test-secret"), nil
	})
	if err != nil {
		t.Fatalf("failed to parse JWT: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if _, exists := claims["role"]; exists {
		t.Fatal("expected no role claim for empty role")
	}
}

func TestUnit_GenerateJWTWithRoleAndScope(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	tokenStr := h.generateJWTWithRoleAndScope("user-3", "user", "basic profile", 30*time.Minute)
	if tokenStr == "" {
		t.Fatal("expected non-empty token")
	}

	parsed, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		return []byte("unit-test-secret"), nil
	})
	if err != nil {
		t.Fatalf("failed to parse JWT: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["sub"] != "user-3" {
		t.Fatalf("expected sub=user-3, got %v", claims["sub"])
	}
	if claims["role"] != "user" {
		t.Fatalf("expected role=user, got %v", claims["role"])
	}
	if claims["scope"] != "basic profile" {
		t.Fatalf("expected scope='basic profile', got %v", claims["scope"])
	}
}

func TestUnit_GenerateJWTWithRoleAndScope_EmptyScope(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	tokenStr := h.generateJWTWithRoleAndScope("user-4", "admin", "", 30*time.Minute)
	if tokenStr == "" {
		t.Fatal("expected non-empty token")
	}

	parsed, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		return []byte("unit-test-secret"), nil
	})
	if err != nil {
		t.Fatalf("failed to parse JWT: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if _, exists := claims["scope"]; exists {
		t.Fatal("expected no scope claim for empty scope")
	}
}

// ---------------------------------------------------------------------------
// Tests: Login
// ---------------------------------------------------------------------------

func TestUnit_Login_Success(t *testing.T) {
	userRepo := newMockUserRepoWithPasswords()
	authRepo := newMockAuthRepo()
	pw := "correct-password"
	hash, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	user := &domain.User{
		ID:       "user-login",
		Username: "loginuser",
		Email:    "login@example.com",
		Role:     domain.RoleUser,
		IsActive: true,
	}
	userRepo.users[user.ID] = user
	userRepo.passwords[user.ID] = string(hash)

	h := newTestAuthHandlers(userRepo, authRepo, nil)

	body, _ := json.Marshal(map[string]string{"email": "login@example.com", "password": pw})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.Login(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeResponse(t, rr)
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	var data map[string]interface{}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if data["access_token"] == nil || data["access_token"].(string) == "" {
		t.Fatal("missing access_token")
	}
	if data["refresh_token"] == nil || data["refresh_token"].(string) == "" {
		t.Fatal("missing refresh_token")
	}
}

func TestUnit_Login_WithUsername_Success(t *testing.T) {
	userRepo := newMockUserRepoWithPasswords()
	authRepo := newMockAuthRepo()
	pw := "correct-password"
	hash, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	user := &domain.User{
		ID:       "user-login-by-username",
		Username: "loginbyusername",
		Email:    "loginbyusername@example.com",
		Role:     domain.RoleUser,
		IsActive: true,
	}
	userRepo.users[user.ID] = user
	userRepo.passwords[user.ID] = string(hash)

	h := newTestAuthHandlers(userRepo, authRepo, nil)

	body, _ := json.Marshal(map[string]string{"username": "loginbyusername", "password": pw})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.Login(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeResponse(t, rr)
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	var data map[string]interface{}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if data["access_token"] == nil || data["access_token"].(string) == "" {
		t.Fatal("missing access_token")
	}
}

func TestUnit_Login_InvalidJSON(t *testing.T) {
	h := newTestAuthHandlers(newMockUserRepo(), nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader("{bad json"))
	rr := httptest.NewRecorder()
	h.Login(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_Login_MissingPassword_WithEmailOnly(t *testing.T) {
	h := newTestAuthHandlers(newMockUserRepo(), nil, nil)
	body, _ := json.Marshal(map[string]string{"email": "a@b.com"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.Login(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_Login_MissingPassword_WithUsernameOnly(t *testing.T) {
	h := newTestAuthHandlers(newMockUserRepo(), nil, nil)
	body, _ := json.Marshal(map[string]string{"username": "someuser"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.Login(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_Login_MissingBothEmailAndUsername(t *testing.T) {
	h := newTestAuthHandlers(newMockUserRepo(), nil, nil)
	body, _ := json.Marshal(map[string]string{"password": "somepassword"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.Login(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_Login_UserNotFound(t *testing.T) {
	h := newTestAuthHandlers(newMockUserRepo(), nil, nil)
	body, _ := json.Marshal(map[string]string{"email": "no@user.com", "password": "pw"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.Login(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_Login_WrongPassword(t *testing.T) {
	userRepo := newMockUserRepoWithPasswords()
	hash, _ := bcrypt.GenerateFromPassword([]byte("real-password"), bcrypt.MinCost)
	user := &domain.User{ID: "u1", Email: "u@e.com", Username: "u"}
	userRepo.users[user.ID] = user
	userRepo.passwords[user.ID] = string(hash)

	h := newTestAuthHandlers(userRepo, nil, nil)
	body, _ := json.Marshal(map[string]string{"email": "u@e.com", "password": "wrong"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.Login(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_Login_NilUserRepo(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	body, _ := json.Marshal(map[string]string{"email": "a@b.com", "password": "pw"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.Login(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

func TestUnit_Login_CreateRefreshTokenError(t *testing.T) {
	userRepo := newMockUserRepoWithPasswords()
	pw := "pass"
	hash, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	user := &domain.User{ID: "u-rt-err", Email: "rt@e.com", Username: "rtuser", Role: domain.RoleUser}
	userRepo.users[user.ID] = user
	userRepo.passwords[user.ID] = string(hash)

	authRepo := newMockAuthRepo()
	authRepo.createRefreshErr = errors.New("db error")

	h := newTestAuthHandlers(userRepo, authRepo, nil)
	body, _ := json.Marshal(map[string]string{"email": "rt@e.com", "password": pw})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.Login(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

func TestUnit_Login_CreateSessionError(t *testing.T) {
	userRepo := newMockUserRepoWithPasswords()
	pw := "pass"
	hash, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	user := &domain.User{ID: "u-se-err", Email: "se@e.com", Username: "seuser", Role: domain.RoleUser}
	userRepo.users[user.ID] = user
	userRepo.passwords[user.ID] = string(hash)

	authRepo := newMockAuthRepo()
	authRepo.createSessionErr = errors.New("session db error")

	h := newTestAuthHandlers(userRepo, authRepo, nil)
	body, _ := json.Marshal(map[string]string{"email": "se@e.com", "password": pw})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.Login(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: Register
// ---------------------------------------------------------------------------

func TestUnit_Register_NilRepo(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	body, _ := json.Marshal(map[string]string{"username": "u", "email": "e@e.com", "password": "p"})
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.Register(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

func TestUnit_Register_InvalidJSON(t *testing.T) {
	h := newTestAuthHandlers(newMockUserRepo(), nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader("not json"))
	rr := httptest.NewRecorder()
	h.Register(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: RefreshToken
// ---------------------------------------------------------------------------

func TestUnit_RefreshToken_Success(t *testing.T) {
	userRepo := newMockUserRepoWithPasswords()
	user := &domain.User{ID: "u-ref", Username: "refuser", Role: domain.RoleUser}
	userRepo.users[user.ID] = user

	authRepo := newMockAuthRepo()
	oldToken := uuid.NewString()
	authRepo.refreshTokens[oldToken] = &usecase.RefreshToken{
		ID:        uuid.NewString(),
		UserID:    user.ID,
		Token:     oldToken,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
		CreatedAt: time.Now(),
	}

	h := newTestAuthHandlers(userRepo, authRepo, nil)
	body, _ := json.Marshal(map[string]string{"refresh_token": oldToken})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.RefreshToken(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeResponse(t, rr)
	var data map[string]interface{}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if data["access_token"] == nil || data["access_token"].(string) == "" {
		t.Fatal("missing access_token")
	}
	newRefresh := data["refresh_token"].(string)
	if newRefresh == "" || newRefresh == oldToken {
		t.Fatal("expected new refresh_token different from old")
	}
}

func TestUnit_RefreshToken_InvalidJSON(t *testing.T) {
	h := newTestAuthHandlers(nil, newMockAuthRepo(), nil)
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", strings.NewReader("{bad"))
	rr := httptest.NewRecorder()
	h.RefreshToken(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_RefreshToken_MissingToken(t *testing.T) {
	h := newTestAuthHandlers(nil, newMockAuthRepo(), nil)
	body, _ := json.Marshal(map[string]string{"refresh_token": ""})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.RefreshToken(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_RefreshToken_NilAuthRepo(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	body, _ := json.Marshal(map[string]string{"refresh_token": "some-token"})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.RefreshToken(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

func TestUnit_RefreshToken_TokenNotFound(t *testing.T) {
	authRepo := newMockAuthRepo()
	h := newTestAuthHandlers(nil, authRepo, nil)
	body, _ := json.Marshal(map[string]string{"refresh_token": "nonexistent"})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.RefreshToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_RefreshToken_CreateRefreshErr(t *testing.T) {
	authRepo := newMockAuthRepo()
	oldToken := uuid.NewString()
	authRepo.refreshTokens[oldToken] = &usecase.RefreshToken{
		ID: uuid.NewString(), UserID: "u1", Token: oldToken,
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}
	authRepo.createRefreshErr = errors.New("db fail")

	h := newTestAuthHandlers(nil, authRepo, nil)
	body, _ := json.Marshal(map[string]string{"refresh_token": oldToken})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.RefreshToken(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

func TestUnit_RefreshToken_CreateSessionErr(t *testing.T) {
	authRepo := newMockAuthRepo()
	oldToken := uuid.NewString()
	authRepo.refreshTokens[oldToken] = &usecase.RefreshToken{
		ID: uuid.NewString(), UserID: "u1", Token: oldToken,
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}
	authRepo.createSessionErr = errors.New("session fail")

	h := newTestAuthHandlers(nil, authRepo, nil)
	body, _ := json.Marshal(map[string]string{"refresh_token": oldToken})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.RefreshToken(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: Logout
// ---------------------------------------------------------------------------

func TestUnit_Logout_Success(t *testing.T) {
	authRepo := newMockAuthRepo()
	h := newTestAuthHandlers(nil, authRepo, nil)
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req = req.WithContext(withUserID(req.Context(), "user-logout"))
	rr := httptest.NewRecorder()
	h.Logout(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestUnit_Logout_Unauthorized(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	rr := httptest.NewRecorder()
	h.Logout(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_Logout_NilAuthRepo(t *testing.T) {
	// Logout should still return 200 even without authRepo (graceful)
	h := newTestAuthHandlers(nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req = req.WithContext(withUserID(req.Context(), "user-logout"))
	rr := httptest.NewRecorder()
	h.Logout(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: GetUserVideos (standalone)
// ---------------------------------------------------------------------------

func TestUnit_GetUserVideos_Success(t *testing.T) {
	validUUID := "123e4567-e89b-12d3-a456-426614174000"
	r := chi.NewRouter()
	r.Get("/{id}", GetUserVideos)

	req := httptest.NewRequest(http.MethodGet, "/"+validUUID, nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	resp := decodeResponse(t, rr)
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	if resp.Meta == nil {
		t.Fatal("expected meta in response")
	}
	if resp.Meta.Total != 2 {
		t.Fatalf("expected 2 videos, got %d", resp.Meta.Total)
	}
}

func TestUnit_GetUserVideos_InvalidUUID(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/{id}", GetUserVideos)

	req := httptest.NewRequest(http.MethodGet, "/not-a-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_GetUserVideos_MissingID(t *testing.T) {
	// When the chi route param is empty, RequireUUIDParam returns 400
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	GetUserVideos(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: UpdateCurrentUserHandler (additional edge cases)
// ---------------------------------------------------------------------------

func TestUnit_UpdateCurrentUser_Unauthorized(t *testing.T) {
	repo := newMockUserRepo()
	body, _ := json.Marshal(map[string]string{"display_name": "X"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/me", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	UpdateCurrentUserHandler(repo).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_UpdateCurrentUser_InvalidJSON(t *testing.T) {
	repo := newMockUserRepo()
	repo.users["u1"] = &domain.User{ID: "u1", Username: "u1"}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/me", strings.NewReader("{bad"))
	req = req.WithContext(withUserID(req.Context(), "u1"))
	rr := httptest.NewRecorder()
	UpdateCurrentUserHandler(repo).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_UpdateCurrentUser_NotFound(t *testing.T) {
	repo := newMockUserRepo()
	body, _ := json.Marshal(map[string]string{"display_name": "X"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/users/me", bytes.NewReader(body))
	req = req.WithContext(withUserID(req.Context(), "nonexistent"))
	rr := httptest.NewRecorder()
	UpdateCurrentUserHandler(repo).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: GetUserHandler (additional edge cases)
// ---------------------------------------------------------------------------

func TestUnit_GetUserHandler_InvalidUUID(t *testing.T) {
	repo := newMockUserRepo()
	r := chi.NewRouter()
	r.Get("/{id}", GetUserHandler(repo))

	req := httptest.NewRequest(http.MethodGet, "/not-a-uuid", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: CreateUserHandler (additional edge cases)
// ---------------------------------------------------------------------------

func TestUnit_CreateUserHandler_InvalidJSON(t *testing.T) {
	handler := CreateUserHandler(newMockUserRepo())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader("not json"))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_CreateUserHandler_WithRole(t *testing.T) {
	repo := newMockUserRepo()
	handler := CreateUserHandler(repo)
	body, _ := json.Marshal(map[string]interface{}{
		"username": "adminuser",
		"email":    "admin@test.com",
		"password": "strongpwd123",
		"role":     "admin",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestUnit_CreateUserHandler_WithIsActive(t *testing.T) {
	repo := newMockUserRepo()
	handler := CreateUserHandler(repo)
	falseVal := false
	body, _ := json.Marshal(map[string]interface{}{
		"username":  "inactiveuser",
		"email":     "inactive@test.com",
		"password":  "strongpwd123",
		"is_active": falseVal,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: OAuthToken
// ---------------------------------------------------------------------------

func TestUnit_OAuthToken_MissingGrantType(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	h := newTestAuthHandlers(nil, nil, oauthRepo)

	values := url.Values{}
	values.Set("client_id", "test-client")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthToken(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_OAuthToken_MissingClientAuth(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, newMockOAuthRepo())
	values := url.Values{}
	values.Set("grant_type", "password")
	// no client_id or Authorization header
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.OAuthToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_OAuthToken_NilOAuthRepo(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	values := url.Values{}
	values.Set("grant_type", "password")
	values.Set("client_id", "test")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthToken(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rr.Code)
	}
}

func TestUnit_OAuthToken_UnknownClient(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	values := url.Values{}
	values.Set("grant_type", "password")
	values.Set("client_id", "unknown-client")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_OAuthToken_ConfidentialClientBadSecret(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	secretHash, _ := bcrypt.GenerateFromPassword([]byte("real-secret"), bcrypt.MinCost)
	hashStr := string(secretHash)
	oauthRepo.clients["conf-client"] = &usecase.OAuthClient{
		ID:               "id1",
		ClientID:         "conf-client",
		ClientSecretHash: &hashStr,
		Name:             "Confidential",
		GrantTypes:       []string{"password"},
		IsConfidential:   true,
		AllowedScopes:    []string{"basic"},
	}

	h := newTestAuthHandlers(nil, nil, oauthRepo)
	values := url.Values{}
	values.Set("grant_type", "password")
	values.Set("client_id", "conf-client")
	values.Set("client_secret", "wrong-secret")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_OAuthToken_UnsupportedGrantType(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["pub-client"] = &usecase.OAuthClient{
		ID:             "id2",
		ClientID:       "pub-client",
		Name:           "Public",
		GrantTypes:     []string{"password"},
		IsConfidential: false,
		AllowedScopes:  []string{"basic"},
	}

	h := newTestAuthHandlers(nil, nil, oauthRepo)
	values := url.Values{}
	values.Set("grant_type", "client_credentials")
	values.Set("client_id", "pub-client")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthToken(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_OAuthToken_PasswordGrant_Success(t *testing.T) {
	userRepo := newMockUserRepoWithPasswords()
	pw := "oauth-password"
	hash, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	user := &domain.User{ID: "u-oauth", Username: "oauthuser", Email: "oauth@e.com", Role: domain.RoleUser}
	userRepo.users[user.ID] = user
	userRepo.passwords[user.ID] = string(hash)

	authRepo := newMockAuthRepo()
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app"] = &usecase.OAuthClient{
		ID: "id3", ClientID: "app", Name: "App",
		GrantTypes: []string{"password"}, IsConfidential: false,
		AllowedScopes: []string{"basic", "profile", "email"},
	}

	h := newTestAuthHandlers(userRepo, authRepo, oauthRepo)
	values := url.Values{}
	values.Set("grant_type", "password")
	values.Set("client_id", "app")
	values.Set("username", "oauth@e.com")
	values.Set("password", pw)
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthToken(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}

	resp := decodeResponse(t, rr)
	var data map[string]interface{}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if data["access_token"] == nil || data["access_token"].(string) == "" {
		t.Fatal("missing access_token")
	}
	if data["token_type"] != "bearer" {
		t.Fatalf("expected token_type=bearer, got %v", data["token_type"])
	}
}

func TestUnit_OAuthToken_PasswordGrant_InvalidCredentials(t *testing.T) {
	userRepo := newMockUserRepoWithPasswords()

	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app"] = &usecase.OAuthClient{
		ID: "id4", ClientID: "app", Name: "App",
		GrantTypes: []string{"password"}, IsConfidential: false,
		AllowedScopes: []string{"basic"},
	}

	h := newTestAuthHandlers(userRepo, nil, oauthRepo)
	values := url.Values{}
	values.Set("grant_type", "password")
	values.Set("client_id", "app")
	values.Set("username", "noone@e.com")
	values.Set("password", "pw")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_OAuthToken_PasswordGrant_MissingUsernamePassword(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app"] = &usecase.OAuthClient{
		ID: "id5", ClientID: "app", Name: "App",
		GrantTypes: []string{"password"}, IsConfidential: false,
	}

	h := newTestAuthHandlers(newMockUserRepo(), nil, oauthRepo)
	values := url.Values{}
	values.Set("grant_type", "password")
	values.Set("client_id", "app")
	// missing username and password
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthToken(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_OAuthToken_PasswordGrant_WithBasicAuth(t *testing.T) {
	userRepo := newMockUserRepoWithPasswords()
	pw := "basic-auth-pw"
	hash, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	user := &domain.User{ID: "u-basic", Username: "basicuser", Email: "basic@e.com", Role: domain.RoleUser}
	userRepo.users[user.ID] = user
	userRepo.passwords[user.ID] = string(hash)

	authRepo := newMockAuthRepo()
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["basic-app"] = &usecase.OAuthClient{
		ID: "id-basic", ClientID: "basic-app", Name: "BasicApp",
		GrantTypes: []string{"password"}, IsConfidential: false,
		AllowedScopes: []string{"basic", "profile", "email"},
	}

	h := newTestAuthHandlers(userRepo, authRepo, oauthRepo)
	values := url.Values{}
	values.Set("grant_type", "password")
	values.Set("username", "basicuser")
	values.Set("password", pw)
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", basicAuth("basic-app", ""))
	rr := httptest.NewRecorder()
	h.OAuthToken(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestUnit_OAuthToken_RefreshGrant_Success(t *testing.T) {
	userRepo := newMockUserRepoWithPasswords()
	user := &domain.User{ID: "u-rg", Username: "rguser", Role: domain.RoleUser}
	userRepo.users[user.ID] = user

	authRepo := newMockAuthRepo()
	oldRefresh := uuid.NewString()
	authRepo.refreshTokens[oldRefresh] = &usecase.RefreshToken{
		ID: uuid.NewString(), UserID: user.ID, Token: oldRefresh,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour), CreatedAt: time.Now(),
	}

	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app"] = &usecase.OAuthClient{
		ID: "id6", ClientID: "app", Name: "App",
		GrantTypes: []string{"refresh_token"}, IsConfidential: false,
	}

	h := newTestAuthHandlers(userRepo, authRepo, oauthRepo)
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("client_id", "app")
	values.Set("refresh_token", oldRefresh)
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthToken(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestUnit_OAuthToken_RefreshGrant_MissingToken(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app"] = &usecase.OAuthClient{
		ID: "id7", ClientID: "app", Name: "App",
		GrantTypes: []string{"refresh_token"}, IsConfidential: false,
	}

	h := newTestAuthHandlers(nil, newMockAuthRepo(), oauthRepo)
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("client_id", "app")
	// no refresh_token
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthToken(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_OAuthToken_RefreshGrant_NilAuthRepo(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app"] = &usecase.OAuthClient{
		ID: "id8", ClientID: "app", Name: "App",
		GrantTypes: []string{"refresh_token"}, IsConfidential: false,
	}

	h := newTestAuthHandlers(nil, nil, oauthRepo)
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("client_id", "app")
	values.Set("refresh_token", "some-token")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthToken(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

func TestUnit_OAuthToken_ConfidentialClient_NilSecretHash(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["bad-conf"] = &usecase.OAuthClient{
		ID:               "id-bad-conf",
		ClientID:         "bad-conf",
		ClientSecretHash: nil, // misconfigured
		Name:             "BadConf",
		GrantTypes:       []string{"password"},
		IsConfidential:   true,
	}

	h := newTestAuthHandlers(nil, nil, oauthRepo)
	values := url.Values{}
	values.Set("grant_type", "password")
	values.Set("client_id", "bad-conf")
	values.Set("client_secret", "anything")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_OAuthToken_ConfidentialClient_EmptySecretHash(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	emptyHash := ""
	oauthRepo.clients["empty-conf"] = &usecase.OAuthClient{
		ID:               "id-empty-conf",
		ClientID:         "empty-conf",
		ClientSecretHash: &emptyHash,
		Name:             "EmptyConf",
		GrantTypes:       []string{"password"},
		IsConfidential:   true,
	}

	h := newTestAuthHandlers(nil, nil, oauthRepo)
	values := url.Values{}
	values.Set("grant_type", "password")
	values.Set("client_id", "empty-conf")
	values.Set("client_secret", "anything")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: OAuthRevoke
// ---------------------------------------------------------------------------

func TestUnit_OAuthRevoke_MissingToken(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app"] = &usecase.OAuthClient{
		ID: "id-rev", ClientID: "app", Name: "App",
	}
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	values := url.Values{}
	values.Set("client_id", "app")
	// no token
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthRevoke(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_OAuthRevoke_MissingClientAuth(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, newMockOAuthRepo())
	values := url.Values{}
	values.Set("token", "some-token")
	req := httptest.NewRequest(http.MethodPost, "/oauth/revoke", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.OAuthRevoke(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_OAuthRevoke_NilOAuthRepo(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	values := url.Values{}
	values.Set("token", "some-token")
	values.Set("client_id", "app")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthRevoke(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rr.Code)
	}
}

func TestUnit_OAuthRevoke_UnknownClient(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	values := url.Values{}
	values.Set("token", "tok")
	values.Set("client_id", "nope")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthRevoke(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_OAuthRevoke_TokenNotFound_Returns200(t *testing.T) {
	// RFC 7009: always 200 even if token unknown
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app"] = &usecase.OAuthClient{
		ID: "id-rev2", ClientID: "app", Name: "App",
	}
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	values := url.Values{}
	values.Set("token", "nonexistent-token")
	values.Set("client_id", "app")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthRevoke(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestUnit_OAuthRevoke_WithRefreshTokenHint(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app"] = &usecase.OAuthClient{
		ID: "id-rev3", ClientID: "app", Name: "App",
	}
	authRepo := newMockAuthRepo()
	refreshTok := uuid.NewString()
	authRepo.refreshTokens[refreshTok] = &usecase.RefreshToken{
		ID: uuid.NewString(), UserID: "u1", Token: refreshTok,
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}

	h := newTestAuthHandlers(nil, authRepo, oauthRepo)
	values := url.Values{}
	values.Set("token", refreshTok)
	values.Set("client_id", "app")
	values.Set("token_type_hint", "refresh_token")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthRevoke(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: OAuthIntrospect
// ---------------------------------------------------------------------------

func TestUnit_OAuthIntrospect_EmptyToken(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app"] = &usecase.OAuthClient{
		ID: "id-intro", ClientID: "app", Name: "App",
	}
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	values := url.Values{}
	values.Set("client_id", "app")
	// no token
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthIntrospect(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	resp := decodeResponse(t, rr)
	var data map[string]interface{}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if data["active"] != false {
		t.Fatalf("expected active=false for empty token")
	}
}

func TestUnit_OAuthIntrospect_MissingClientAuth(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, newMockOAuthRepo())
	values := url.Values{}
	values.Set("token", "some-token")
	req := httptest.NewRequest(http.MethodPost, "/oauth/introspect", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.OAuthIntrospect(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_OAuthIntrospect_NilOAuthRepo(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	values := url.Values{}
	values.Set("token", "tok")
	values.Set("client_id", "app")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthIntrospect(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rr.Code)
	}
}

func TestUnit_OAuthIntrospect_ActiveToken(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app"] = &usecase.OAuthClient{
		ID: "id-intro2", ClientID: "app", Name: "App",
	}
	// Store an access token
	tokenValue := "my-access-token"
	tokenHash := hashToken(tokenValue)
	oauthRepo.accessTokens[tokenHash] = &usecase.OAuthAccessToken{
		ID:        uuid.NewString(),
		TokenHash: tokenHash,
		ClientID:  "app",
		UserID:    "user-1",
		Scope:     "basic profile",
		ExpiresAt: time.Now().Add(15 * time.Minute),
		CreatedAt: time.Now(),
		RevokedAt: nil,
	}

	h := newTestAuthHandlers(nil, nil, oauthRepo)
	values := url.Values{}
	values.Set("token", tokenValue)
	values.Set("client_id", "app")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthIntrospect(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	resp := decodeResponse(t, rr)
	var data map[string]interface{}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if data["active"] != true {
		t.Fatalf("expected active=true, got %v", data["active"])
	}
	if data["scope"] != "basic profile" {
		t.Fatalf("expected scope='basic profile', got %v", data["scope"])
	}
}

func TestUnit_OAuthIntrospect_RevokedToken(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app"] = &usecase.OAuthClient{
		ID: "id-intro3", ClientID: "app", Name: "App",
	}
	tokenValue := "revoked-token"
	tokenHash := hashToken(tokenValue)
	now := time.Now()
	oauthRepo.accessTokens[tokenHash] = &usecase.OAuthAccessToken{
		ID:        uuid.NewString(),
		TokenHash: tokenHash,
		ClientID:  "app",
		UserID:    "user-1",
		Scope:     "basic",
		ExpiresAt: time.Now().Add(15 * time.Minute),
		CreatedAt: time.Now(),
		RevokedAt: &now,
	}

	h := newTestAuthHandlers(nil, nil, oauthRepo)
	values := url.Values{}
	values.Set("token", tokenValue)
	values.Set("client_id", "app")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthIntrospect(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	resp := decodeResponse(t, rr)
	var data map[string]interface{}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if data["active"] != false {
		t.Fatal("expected active=false for revoked token")
	}
}

func TestUnit_OAuthIntrospect_ExpiredToken(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app"] = &usecase.OAuthClient{
		ID: "id-intro4", ClientID: "app", Name: "App",
	}
	tokenValue := "expired-access-token"
	tokenHash := hashToken(tokenValue)
	oauthRepo.accessTokens[tokenHash] = &usecase.OAuthAccessToken{
		ID:        uuid.NewString(),
		TokenHash: tokenHash,
		ClientID:  "app",
		UserID:    "user-1",
		Scope:     "basic",
		ExpiresAt: time.Now().Add(-1 * time.Hour), // expired
		CreatedAt: time.Now().Add(-2 * time.Hour),
		RevokedAt: nil,
	}

	h := newTestAuthHandlers(nil, nil, oauthRepo)
	values := url.Values{}
	values.Set("token", tokenValue)
	values.Set("client_id", "app")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthIntrospect(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	resp := decodeResponse(t, rr)
	var data map[string]interface{}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if data["active"] != false {
		t.Fatal("expected active=false for expired token")
	}
}

func TestUnit_OAuthIntrospect_WrongClient(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app1"] = &usecase.OAuthClient{
		ID: "id-intro5", ClientID: "app1", Name: "App1",
	}
	oauthRepo.clients["app2"] = &usecase.OAuthClient{
		ID: "id-intro6", ClientID: "app2", Name: "App2",
	}
	tokenValue := "token-for-app2"
	tokenHash := hashToken(tokenValue)
	oauthRepo.accessTokens[tokenHash] = &usecase.OAuthAccessToken{
		ID:        uuid.NewString(),
		TokenHash: tokenHash,
		ClientID:  "app2",
		UserID:    "user-1",
		Scope:     "basic",
		ExpiresAt: time.Now().Add(15 * time.Minute),
		CreatedAt: time.Now(),
	}

	h := newTestAuthHandlers(nil, nil, oauthRepo)
	// app1 tries to introspect app2's token
	values := url.Values{}
	values.Set("token", tokenValue)
	values.Set("client_id", "app1")
	req := oauthFormRequest(values)
	rr := httptest.NewRecorder()
	h.OAuthIntrospect(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	resp := decodeResponse(t, rr)
	var data map[string]interface{}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if data["active"] != false {
		t.Fatal("expected active=false when queried by different client")
	}
}

// ---------------------------------------------------------------------------
// Tests: OAuthAuthorize
// ---------------------------------------------------------------------------

func TestUnit_OAuthAuthorize_POST_NoUser(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	values := url.Values{}
	values.Set("client_id", "app")
	values.Set("redirect_uri", "https://app.com/callback")
	values.Set("response_type", "code")
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.OAuthAuthorize(rr, req)
	// Should redirect to login
	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "/auth/login") {
		t.Fatalf("expected redirect to /auth/login, got %s", loc)
	}
}

func TestUnit_OAuthAuthorize_MethodNotAllowed(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	req := httptest.NewRequest(http.MethodPut, "/oauth/authorize", nil)
	rr := httptest.NewRecorder()
	h.OAuthAuthorize(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestUnit_OAuthAuthorize_POST_MissingParams(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	// Set up user context with a valid UUID format
	userUUID := uuid.NewString()
	values := url.Values{}
	// Missing client_id, redirect_uri
	values.Set("response_type", "code")
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userUUID))
	rr := httptest.NewRecorder()
	h.OAuthAuthorize(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_OAuthAuthorize_POST_NilOAuthRepo(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	userUUID := uuid.NewString()
	values := url.Values{}
	values.Set("client_id", "app")
	values.Set("redirect_uri", "https://app.com/cb")
	values.Set("response_type", "code")
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userUUID))
	rr := httptest.NewRecorder()
	h.OAuthAuthorize(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rr.Code)
	}
}

func TestUnit_OAuthAuthorize_POST_AccessDenied(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app"] = &usecase.OAuthClient{
		ID: "id-auth", ClientID: "app", Name: "App",
		GrantTypes:    []string{"authorization_code"},
		RedirectURIs:  []string{"https://app.com/cb"},
		AllowedScopes: []string{"basic"},
	}
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	userUUID := uuid.NewString()
	values := url.Values{}
	values.Set("client_id", "app")
	values.Set("redirect_uri", "https://app.com/cb")
	values.Set("response_type", "code")
	values.Set("scope", "basic")
	values.Set("approve", "false") // denied
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userUUID))
	rr := httptest.NewRecorder()
	h.OAuthAuthorize(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "error=access_denied") {
		t.Fatalf("expected access_denied in redirect, got %s", loc)
	}
}

func TestUnit_OAuthAuthorize_POST_InvalidRedirectURI(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app"] = &usecase.OAuthClient{
		ID: "id-authbad", ClientID: "app", Name: "App",
		GrantTypes:    []string{"authorization_code"},
		RedirectURIs:  []string{"https://legit.com/cb"},
		AllowedScopes: []string{"basic"},
	}
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	userUUID := uuid.NewString()
	values := url.Values{}
	values.Set("client_id", "app")
	values.Set("redirect_uri", "https://evil.com/cb") // not in allowed list
	values.Set("response_type", "code")
	values.Set("scope", "basic")
	values.Set("approve", "true")
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userUUID))
	rr := httptest.NewRecorder()
	h.OAuthAuthorize(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_OAuthAuthorize_POST_Success(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["app"] = &usecase.OAuthClient{
		ID: "id-auth-ok", ClientID: "app", Name: "App",
		GrantTypes:    []string{"authorization_code"},
		RedirectURIs:  []string{"https://app.com/cb"},
		AllowedScopes: []string{"basic", "profile"},
	}
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	userUUID := uuid.NewString()
	values := url.Values{}
	values.Set("client_id", "app")
	values.Set("redirect_uri", "https://app.com/cb")
	values.Set("response_type", "code")
	values.Set("scope", "basic")
	values.Set("state", "xyz")
	values.Set("approve", "true")
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userUUID))
	rr := httptest.NewRecorder()
	h.OAuthAuthorize(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "code=") {
		t.Fatalf("expected code= in redirect, got %s", loc)
	}
	if !strings.Contains(loc, "state=xyz") {
		t.Fatalf("expected state=xyz in redirect, got %s", loc)
	}
}

// ---------------------------------------------------------------------------
// Tests: Admin OAuth endpoints
// ---------------------------------------------------------------------------

func TestUnit_AdminListOAuthClients_NilRepo(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/oauth/clients", nil)
	rr := httptest.NewRecorder()
	h.AdminListOAuthClients(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rr.Code)
	}
}

func TestUnit_AdminListOAuthClients_Success(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["c1"] = &usecase.OAuthClient{
		ID: "id1", ClientID: "c1", Name: "Client1",
		GrantTypes: []string{"password"}, IsConfidential: true,
	}
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	req := httptest.NewRequest(http.MethodGet, "/admin/oauth/clients", nil)
	rr := httptest.NewRecorder()
	h.AdminListOAuthClients(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestUnit_AdminListOAuthClients_DBError(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.listClientsErr = errors.New("db error")
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	req := httptest.NewRequest(http.MethodGet, "/admin/oauth/clients", nil)
	rr := httptest.NewRecorder()
	h.AdminListOAuthClients(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

func TestUnit_AdminCreateOAuthClient_NilRepo(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	body, _ := json.Marshal(map[string]string{"client_id": "x", "name": "X"})
	req := httptest.NewRequest(http.MethodPost, "/admin/oauth/clients", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.AdminCreateOAuthClient(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rr.Code)
	}
}

func TestUnit_AdminCreateOAuthClient_InvalidJSON(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, newMockOAuthRepo())
	req := httptest.NewRequest(http.MethodPost, "/admin/oauth/clients", strings.NewReader("bad"))
	rr := httptest.NewRecorder()
	h.AdminCreateOAuthClient(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_AdminCreateOAuthClient_MissingFields(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, newMockOAuthRepo())
	body, _ := json.Marshal(map[string]string{"name": "X"}) // missing client_id
	req := httptest.NewRequest(http.MethodPost, "/admin/oauth/clients", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.AdminCreateOAuthClient(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_AdminCreateOAuthClient_ConfidentialMissingSecret(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, newMockOAuthRepo())
	isConf := true
	body, _ := json.Marshal(map[string]interface{}{
		"client_id":       "conf-no-secret",
		"name":            "Conf",
		"is_confidential": isConf,
		// no client_secret
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/oauth/clients", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.AdminCreateOAuthClient(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_AdminCreateOAuthClient_Success(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	body, _ := json.Marshal(map[string]interface{}{
		"client_id":     "new-client",
		"client_secret": "secret123",
		"name":          "NewClient",
		"grant_types":   []string{"password", "refresh_token"},
		"scopes":        []string{"basic", "profile"},
		"redirect_uris": []string{"https://example.com/cb"},
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/oauth/clients", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.AdminCreateOAuthClient(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestUnit_AdminCreateOAuthClient_PublicClient(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	isConf := false
	body, _ := json.Marshal(map[string]interface{}{
		"client_id":       "pub-client",
		"name":            "PublicClient",
		"is_confidential": isConf,
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/oauth/clients", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.AdminCreateOAuthClient(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestUnit_AdminCreateOAuthClient_CreateError(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.createClientErr = errors.New("duplicate")
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	isConf := false
	body, _ := json.Marshal(map[string]interface{}{
		"client_id":       "err-client",
		"name":            "ErrClient",
		"is_confidential": isConf,
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/oauth/clients", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.AdminCreateOAuthClient(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_AdminRotateOAuthClientSecret_NilRepo(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("clientId", "c1")
	body, _ := json.Marshal(map[string]string{"client_secret": "newsecret"})
	req := httptest.NewRequest(http.MethodPut, "/admin/oauth/clients/c1/secret", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.AdminRotateOAuthClientSecret(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rr.Code)
	}
}

func TestUnit_AdminRotateOAuthClientSecret_MissingClientId(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, newMockOAuthRepo())
	rctx := chi.NewRouteContext()
	// empty clientId
	body, _ := json.Marshal(map[string]string{"client_secret": "newsecret"})
	req := httptest.NewRequest(http.MethodPut, "/admin/oauth/clients//secret", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.AdminRotateOAuthClientSecret(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_AdminRotateOAuthClientSecret_InvalidJSON(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, newMockOAuthRepo())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("clientId", "c1")
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader("bad"))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.AdminRotateOAuthClientSecret(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_AdminRotateOAuthClientSecret_ConfMissingSecret(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, newMockOAuthRepo())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("clientId", "c1")
	isConf := true
	body, _ := json.Marshal(map[string]interface{}{
		"is_confidential": isConf,
		// no client_secret
	})
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.AdminRotateOAuthClientSecret(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_AdminRotateOAuthClientSecret_Success(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["c1"] = &usecase.OAuthClient{
		ID: "id-c1", ClientID: "c1", Name: "C1", IsConfidential: true,
	}
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("clientId", "c1")
	body, _ := json.Marshal(map[string]string{"client_secret": "new-secret-value"})
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.AdminRotateOAuthClientSecret(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestUnit_AdminRotateOAuthClientSecret_PublicClient(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["pub"] = &usecase.OAuthClient{
		ID: "id-pub", ClientID: "pub", Name: "Pub", IsConfidential: false,
	}
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("clientId", "pub")
	isConf := false
	body, _ := json.Marshal(map[string]interface{}{
		"is_confidential": isConf,
	})
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.AdminRotateOAuthClientSecret(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestUnit_AdminDeleteOAuthClient_NilRepo(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("clientId", "c1")
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.AdminDeleteOAuthClient(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rr.Code)
	}
}

func TestUnit_AdminDeleteOAuthClient_MissingClientId(t *testing.T) {
	h := newTestAuthHandlers(nil, nil, newMockOAuthRepo())
	rctx := chi.NewRouteContext()
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.AdminDeleteOAuthClient(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_AdminDeleteOAuthClient_Success(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.clients["todelete"] = &usecase.OAuthClient{
		ID: "id-del", ClientID: "todelete", Name: "Del",
	}
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("clientId", "todelete")
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.AdminDeleteOAuthClient(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestUnit_AdminDeleteOAuthClient_Error(t *testing.T) {
	oauthRepo := newMockOAuthRepo()
	oauthRepo.deleteClientErr = errors.New("delete failed")
	h := newTestAuthHandlers(nil, nil, oauthRepo)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("clientId", "c1")
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.AdminDeleteOAuthClient(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: TwoFAHandlers
// ---------------------------------------------------------------------------

func TestUnit_SetupTwoFA_Unauthorized(t *testing.T) {
	svc := usecase.NewTwoFAService(newMockUserRepo(), newMockBackupCodeRepo(), "Vidra Core")
	h := NewTwoFAHandlers(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/setup", nil)
	rr := httptest.NewRecorder()
	h.SetupTwoFA(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_SetupTwoFA_AlreadyEnabled(t *testing.T) {
	userRepo := newMockUserRepo()
	user := &domain.User{
		ID:           "u-2fa",
		Username:     "twofauser",
		Email:        "2fa@e.com",
		TwoFAEnabled: true,
	}
	userRepo.users[user.ID] = user

	svc := usecase.NewTwoFAService(userRepo, newMockBackupCodeRepo(), "Vidra Core")
	h := NewTwoFAHandlers(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/setup", nil)
	req = req.WithContext(withUserID(req.Context(), user.ID))
	rr := httptest.NewRecorder()
	h.SetupTwoFA(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rr.Code)
	}
}

func TestUnit_SetupTwoFA_Success(t *testing.T) {
	userRepo := newMockUserRepo()
	user := &domain.User{
		ID:           "u-2fa-setup",
		Username:     "newtwofauser",
		Email:        "new2fa@e.com",
		TwoFAEnabled: false,
	}
	userRepo.users[user.ID] = user

	svc := usecase.NewTwoFAService(userRepo, newMockBackupCodeRepo(), "Vidra Core")
	h := NewTwoFAHandlers(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/setup", nil)
	req = req.WithContext(withUserID(req.Context(), user.ID))
	rr := httptest.NewRecorder()
	h.SetupTwoFA(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestUnit_VerifyTwoFASetup_Unauthorized(t *testing.T) {
	svc := usecase.NewTwoFAService(newMockUserRepo(), newMockBackupCodeRepo(), "Vidra Core")
	h := NewTwoFAHandlers(svc)
	body, _ := json.Marshal(map[string]string{"code": "123456"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/verify-setup", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.VerifyTwoFASetup(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_VerifyTwoFASetup_InvalidJSON(t *testing.T) {
	svc := usecase.NewTwoFAService(newMockUserRepo(), newMockBackupCodeRepo(), "Vidra Core")
	h := NewTwoFAHandlers(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/verify-setup", strings.NewReader("bad"))
	req = req.WithContext(withUserID(req.Context(), "u1"))
	rr := httptest.NewRecorder()
	h.VerifyTwoFASetup(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_VerifyTwoFASetup_MissingCode(t *testing.T) {
	svc := usecase.NewTwoFAService(newMockUserRepo(), newMockBackupCodeRepo(), "Vidra Core")
	h := NewTwoFAHandlers(svc)
	body, _ := json.Marshal(map[string]string{"code": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/verify-setup", bytes.NewReader(body))
	req = req.WithContext(withUserID(req.Context(), "u1"))
	rr := httptest.NewRecorder()
	h.VerifyTwoFASetup(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_DisableTwoFA_Unauthorized(t *testing.T) {
	svc := usecase.NewTwoFAService(newMockUserRepo(), newMockBackupCodeRepo(), "Vidra Core")
	h := NewTwoFAHandlers(svc)
	body, _ := json.Marshal(map[string]string{"password": "pw", "code": "123456"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/disable", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.DisableTwoFA(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_DisableTwoFA_InvalidJSON(t *testing.T) {
	svc := usecase.NewTwoFAService(newMockUserRepo(), newMockBackupCodeRepo(), "Vidra Core")
	h := NewTwoFAHandlers(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/disable", strings.NewReader("bad"))
	req = req.WithContext(withUserID(req.Context(), "u1"))
	rr := httptest.NewRecorder()
	h.DisableTwoFA(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_DisableTwoFA_MissingFields(t *testing.T) {
	svc := usecase.NewTwoFAService(newMockUserRepo(), newMockBackupCodeRepo(), "Vidra Core")
	h := NewTwoFAHandlers(svc)
	body, _ := json.Marshal(map[string]string{"password": ""}) // missing code too
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/disable", bytes.NewReader(body))
	req = req.WithContext(withUserID(req.Context(), "u1"))
	rr := httptest.NewRecorder()
	h.DisableTwoFA(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_RegenerateBackupCodes_Unauthorized(t *testing.T) {
	svc := usecase.NewTwoFAService(newMockUserRepo(), newMockBackupCodeRepo(), "Vidra Core")
	h := NewTwoFAHandlers(svc)
	body, _ := json.Marshal(map[string]string{"code": "123456"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/regenerate-backup-codes", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.RegenerateBackupCodes(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_RegenerateBackupCodes_InvalidJSON(t *testing.T) {
	svc := usecase.NewTwoFAService(newMockUserRepo(), newMockBackupCodeRepo(), "Vidra Core")
	h := NewTwoFAHandlers(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/regenerate-backup-codes", strings.NewReader("bad"))
	req = req.WithContext(withUserID(req.Context(), "u1"))
	rr := httptest.NewRecorder()
	h.RegenerateBackupCodes(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_RegenerateBackupCodes_MissingCode(t *testing.T) {
	svc := usecase.NewTwoFAService(newMockUserRepo(), newMockBackupCodeRepo(), "Vidra Core")
	h := NewTwoFAHandlers(svc)
	body, _ := json.Marshal(map[string]string{"code": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/regenerate-backup-codes", bytes.NewReader(body))
	req = req.WithContext(withUserID(req.Context(), "u1"))
	rr := httptest.NewRecorder()
	h.RegenerateBackupCodes(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_GetTwoFAStatus_Unauthorized(t *testing.T) {
	svc := usecase.NewTwoFAService(newMockUserRepo(), newMockBackupCodeRepo(), "Vidra Core")
	h := NewTwoFAHandlers(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/2fa/status", nil)
	rr := httptest.NewRecorder()
	h.GetTwoFAStatus(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_GetTwoFAStatus_Success(t *testing.T) {
	userRepo := newMockUserRepo()
	user := &domain.User{
		ID:           "u-status",
		Username:     "statususer",
		TwoFAEnabled: false,
	}
	userRepo.users[user.ID] = user

	svc := usecase.NewTwoFAService(userRepo, newMockBackupCodeRepo(), "Vidra Core")
	h := NewTwoFAHandlers(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/2fa/status", nil)
	req = req.WithContext(withUserID(req.Context(), user.ID))
	rr := httptest.NewRecorder()
	h.GetTwoFAStatus(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Tests: EmailVerificationHandlers (unit-level, handler edge cases)
// ---------------------------------------------------------------------------

func TestUnit_VerifyEmail_InvalidJSON(t *testing.T) {
	svc := usecase.NewEmailVerificationService(newMockUserRepo(), &mockVerificationRepo{tokens: make(map[string]*domain.EmailVerificationToken)}, nil)
	h := NewEmailVerificationHandlers(svc)
	req := httptest.NewRequest(http.MethodPost, "/verify-email", strings.NewReader("bad json"))
	rr := httptest.NewRecorder()
	h.VerifyEmail(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_VerifyEmail_MissingTokenAndCode(t *testing.T) {
	svc := usecase.NewEmailVerificationService(newMockUserRepo(), &mockVerificationRepo{tokens: make(map[string]*domain.EmailVerificationToken)}, nil)
	h := NewEmailVerificationHandlers(svc)
	body, _ := json.Marshal(map[string]string{})
	req := httptest.NewRequest(http.MethodPost, "/verify-email", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.VerifyEmail(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_VerifyEmail_CodeWithoutAuth(t *testing.T) {
	svc := usecase.NewEmailVerificationService(newMockUserRepo(), &mockVerificationRepo{tokens: make(map[string]*domain.EmailVerificationToken)}, nil)
	h := NewEmailVerificationHandlers(svc)
	body, _ := json.Marshal(map[string]string{"code": "123456"})
	req := httptest.NewRequest(http.MethodPost, "/verify-email", bytes.NewReader(body))
	// no user in context
	rr := httptest.NewRecorder()
	h.VerifyEmail(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_ResendVerification_InvalidJSON(t *testing.T) {
	svc := usecase.NewEmailVerificationService(newMockUserRepo(), &mockVerificationRepo{tokens: make(map[string]*domain.EmailVerificationToken)}, nil)
	h := NewEmailVerificationHandlers(svc)
	req := httptest.NewRequest(http.MethodPost, "/resend", strings.NewReader("bad"))
	rr := httptest.NewRecorder()
	h.ResendVerification(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_ResendVerification_MissingEmail(t *testing.T) {
	svc := usecase.NewEmailVerificationService(newMockUserRepo(), &mockVerificationRepo{tokens: make(map[string]*domain.EmailVerificationToken)}, nil)
	h := NewEmailVerificationHandlers(svc)
	body, _ := json.Marshal(map[string]string{"email": ""})
	req := httptest.NewRequest(http.MethodPost, "/resend", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ResendVerification(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestUnit_ResendVerification_NonexistentEmail(t *testing.T) {
	svc := usecase.NewEmailVerificationService(newMockUserRepo(), &mockVerificationRepo{tokens: make(map[string]*domain.EmailVerificationToken)}, nil)
	h := NewEmailVerificationHandlers(svc)
	body, _ := json.Marshal(map[string]string{"email": "noone@nowhere.com"})
	req := httptest.NewRequest(http.MethodPost, "/resend", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ResendVerification(rr, req)
	// Should still return 200 for security (don't reveal email existence)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestUnit_GetVerificationStatus_Unauthorized(t *testing.T) {
	svc := usecase.NewEmailVerificationService(newMockUserRepo(), &mockVerificationRepo{tokens: make(map[string]*domain.EmailVerificationToken)}, nil)
	h := NewEmailVerificationHandlers(svc)
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rr := httptest.NewRecorder()
	h.GetVerificationStatus(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUnit_GetVerificationStatus_Success(t *testing.T) {
	svc := usecase.NewEmailVerificationService(newMockUserRepo(), &mockVerificationRepo{tokens: make(map[string]*domain.EmailVerificationToken)}, nil)
	h := NewEmailVerificationHandlers(svc)
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req = req.WithContext(withUserID(req.Context(), "some-user-id"))
	rr := httptest.NewRecorder()
	h.GetVerificationStatus(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: Helper functions (exported from oauth.go)
// ---------------------------------------------------------------------------

func TestUnit_VerifyPKCE_Plain(t *testing.T) {
	if !verifyPKCE("challenge-value", "challenge-value", "plain") {
		t.Fatal("plain PKCE should match")
	}
	if verifyPKCE("wrong", "challenge-value", "plain") {
		t.Fatal("plain PKCE should not match with wrong verifier")
	}
}

func TestUnit_VerifyPKCE_S256(t *testing.T) {
	// The challenge is base64url(sha256(verifier))
	verifier := "test-code-verifier"
	challenge := computeS256Challenge(verifier)
	if !verifyPKCE(verifier, challenge, "S256") {
		t.Fatalf("S256 PKCE should match: verifier=%s challenge=%s", verifier, challenge)
	}
	if verifyPKCE("wrong-verifier", challenge, "S256") {
		t.Fatal("S256 PKCE should not match with wrong verifier")
	}
}

func TestUnit_VerifyPKCE_EmptyMethod(t *testing.T) {
	// Empty method defaults to plain
	if !verifyPKCE("value", "value", "") {
		t.Fatal("empty method should behave like plain")
	}
}

func TestUnit_VerifyPKCE_UnsupportedMethod(t *testing.T) {
	if verifyPKCE("v", "c", "SHA512") {
		t.Fatal("unsupported method should return false")
	}
}

func TestUnit_IsValidRedirectURI(t *testing.T) {
	allowed := []string{"https://app.com/cb", "https://other.com/cb"}
	if !isValidRedirectURI("https://app.com/cb", allowed) {
		t.Fatal("expected valid")
	}
	if isValidRedirectURI("https://evil.com/cb", allowed) {
		t.Fatal("expected invalid")
	}
	if isValidRedirectURI("https://app.com/cb", nil) {
		t.Fatal("expected invalid for nil allowed list")
	}
}

func TestUnit_ParseScopes(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"", []string{"basic"}},
		{"read write", []string{"read", "write"}},
		{"profile", []string{"profile"}},
	}
	for _, tc := range cases {
		got := parseScopes(tc.input)
		if len(got) != len(tc.want) {
			t.Fatalf("parseScopes(%q): got %v, want %v", tc.input, got, tc.want)
		}
		for i, v := range got {
			if v != tc.want[i] {
				t.Fatalf("parseScopes(%q)[%d]: got %q, want %q", tc.input, i, v, tc.want[i])
			}
		}
	}
}

func TestUnit_ValidateScopes(t *testing.T) {
	allowed := []string{"basic", "profile", "email"}
	if !validateScopes([]string{"basic", "profile"}, allowed) {
		t.Fatal("expected valid scopes")
	}
	if validateScopes([]string{"basic", "admin"}, allowed) {
		t.Fatal("expected invalid scope 'admin'")
	}
	if !validateScopes([]string{}, allowed) {
		t.Fatal("empty requested scopes should be valid")
	}
}

func TestUnit_ContainsGrantType(t *testing.T) {
	grants := []string{"password", "refresh_token", "authorization_code"}
	if !containsGrantType(grants, "password") {
		t.Fatal("expected to find password")
	}
	if containsGrantType(grants, "client_credentials") {
		t.Fatal("should not find client_credentials")
	}
}

func TestUnit_HashToken(t *testing.T) {
	h1 := hashToken("abc")
	h2 := hashToken("abc")
	if h1 != h2 {
		t.Fatal("same input should produce same hash")
	}
	h3 := hashToken("xyz")
	if h1 == h3 {
		t.Fatal("different inputs should produce different hashes")
	}
	if len(h1) != 64 {
		t.Fatalf("expected 64 char hex hash, got %d", len(h1))
	}
}

// computeS256Challenge is a test helper that computes a PKCE S256 challenge.
func computeS256Challenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(h[:])
}
