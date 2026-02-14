package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/port"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

type mockUserRepository struct {
	users              map[string]*domain.User
	passwordHashes     map[string]string
	getByEmailErr      error
	getByUsernameErr   error
	getPasswordHashErr error
}

func newMockUserRepository() *mockUserRepository {
	return &mockUserRepository{
		users:          make(map[string]*domain.User),
		passwordHashes: make(map[string]string),
	}
}

func (m *mockUserRepository) addUser(user *domain.User, password string) error {
	m.users[user.ID] = user
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	m.passwordHashes[user.ID] = string(hash)
	return nil
}

func (m *mockUserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	if m.getByEmailErr != nil {
		return nil, m.getByEmailErr
	}
	for _, u := range m.users {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (m *mockUserRepository) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	if m.getByUsernameErr != nil {
		return nil, m.getByUsernameErr
	}
	for _, u := range m.users {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (m *mockUserRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	u, ok := m.users[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return u, nil
}

func (m *mockUserRepository) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	if m.getPasswordHashErr != nil {
		return "", m.getPasswordHashErr
	}
	hash, ok := m.passwordHashes[userID]
	if !ok {
		return "", domain.ErrNotFound
	}
	return hash, nil
}

func (m *mockUserRepository) Create(ctx context.Context, user *domain.User, passwordHash string) error {
	return nil
}
func (m *mockUserRepository) Update(ctx context.Context, user *domain.User) error { return nil }
func (m *mockUserRepository) Delete(ctx context.Context, id string) error         { return nil }
func (m *mockUserRepository) List(ctx context.Context, limit, offset int) ([]*domain.User, error) {
	return nil, nil
}
func (m *mockUserRepository) GetUserDisplayName(ctx context.Context, userID string) (string, error) {
	return "", nil
}
func (m *mockUserRepository) Count(ctx context.Context) (int64, error) { return 0, nil }
func (m *mockUserRepository) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	return nil
}
func (m *mockUserRepository) SetAvatarFields(ctx context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error {
	return nil
}
func (m *mockUserRepository) MarkEmailAsVerified(ctx context.Context, userID string) error {
	return nil
}

type mockAuthRepository struct {
	refreshTokens         map[string]*port.RefreshToken
	sessions              map[string]string
	createRefreshTokenErr error
	createSessionErr      error
}

func newMockAuthRepository() *mockAuthRepository {
	return &mockAuthRepository{
		refreshTokens: make(map[string]*port.RefreshToken),
		sessions:      make(map[string]string),
	}
}

func (m *mockAuthRepository) CreateRefreshToken(ctx context.Context, rt *port.RefreshToken) error {
	if m.createRefreshTokenErr != nil {
		return m.createRefreshTokenErr
	}
	m.refreshTokens[rt.Token] = rt
	return nil
}

func (m *mockAuthRepository) GetRefreshToken(ctx context.Context, token string) (*port.RefreshToken, error) {
	rt, ok := m.refreshTokens[token]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if time.Now().After(rt.ExpiresAt) {
		return nil, errors.New("token expired")
	}
	return rt, nil
}

func (m *mockAuthRepository) RevokeRefreshToken(ctx context.Context, token string) error {
	delete(m.refreshTokens, token)
	return nil
}

func (m *mockAuthRepository) RevokeAllUserTokens(ctx context.Context, userID string) error {
	return nil
}

func (m *mockAuthRepository) CreateSession(ctx context.Context, sessionToken, userID string, expiresAt time.Time) error {
	if m.createSessionErr != nil {
		return m.createSessionErr
	}
	m.sessions[sessionToken] = userID
	return nil
}

func (m *mockAuthRepository) DeleteSession(ctx context.Context, sessionToken string) error {
	delete(m.sessions, sessionToken)
	return nil
}

func (m *mockAuthRepository) GetSession(ctx context.Context, sessionID string) (string, error) {
	userID, ok := m.sessions[sessionID]
	if !ok {
		return "", domain.ErrNotFound
	}
	return userID, nil
}
func (m *mockAuthRepository) DeleteAllUserSessions(ctx context.Context, userID string) error {
	return nil
}
func (m *mockAuthRepository) CleanExpiredTokens(ctx context.Context) error { return nil }

type mockOAuthRepository struct {
	clients map[string]*port.OAuthClient
}

func newMockOAuthRepository() *mockOAuthRepository {
	return &mockOAuthRepository{
		clients: make(map[string]*port.OAuthClient),
	}
}

func (m *mockOAuthRepository) addClient(client *port.OAuthClient) {
	m.clients[client.ClientID] = client
}

func (m *mockOAuthRepository) GetClientByClientID(ctx context.Context, clientID string) (*port.OAuthClient, error) {
	client, ok := m.clients[clientID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return client, nil
}

func (m *mockOAuthRepository) CreateClient(ctx context.Context, client *port.OAuthClient) error {
	return nil
}
func (m *mockOAuthRepository) UpdateClientSecret(ctx context.Context, clientID string, secretHash *string, isConfidential bool) error {
	return nil
}
func (m *mockOAuthRepository) DeleteClient(ctx context.Context, clientID string) error { return nil }
func (m *mockOAuthRepository) ListClients(ctx context.Context) ([]*port.OAuthClient, error) {
	return nil, nil
}
func (m *mockOAuthRepository) CreateAccessToken(ctx context.Context, at *port.OAuthAccessToken) error {
	return nil
}
func (m *mockOAuthRepository) GetAccessToken(ctx context.Context, tokenHash string) (*port.OAuthAccessToken, error) {
	return nil, nil
}
func (m *mockOAuthRepository) RevokeAccessToken(ctx context.Context, tokenHash string) error {
	return nil
}
func (m *mockOAuthRepository) ListUserTokens(ctx context.Context, userID string) ([]*port.OAuthAccessToken, error) {
	return nil, nil
}
func (m *mockOAuthRepository) DeleteExpiredTokens(ctx context.Context) error { return nil }
func (m *mockOAuthRepository) CreateAuthorizationCode(ctx context.Context, code *port.OAuthAuthorizationCode) error {
	return nil
}
func (m *mockOAuthRepository) GetAuthorizationCode(ctx context.Context, code string) (*port.OAuthAuthorizationCode, error) {
	return nil, nil
}
func (m *mockOAuthRepository) MarkCodeAsUsed(ctx context.Context, code string) error { return nil }
func (m *mockOAuthRepository) DeleteExpiredCodes(ctx context.Context) error          { return nil }

func setupOAuthTest(t *testing.T) (*AuthHandlers, *mockUserRepository, *mockAuthRepository, *mockOAuthRepository) {
	t.Helper()

	userRepo := newMockUserRepository()
	authRepo := newMockAuthRepository()
	oauthRepo := newMockOAuthRepository()

	h := NewAuthHandlers(
		userRepo,
		authRepo,
		oauthRepo,
		nil,
		"test-jwt-secret",
		nil,
		0,
		"",
		"",
		nil,
	)

	return h, userRepo, authRepo, oauthRepo
}

func TestOAuthToken_PasswordGrant_Success(t *testing.T) {
	h, userRepo, authRepo, oauthRepo := setupOAuthTest(t)

	userID := uuid.NewString()
	user := &domain.User{
		ID:       userID,
		Username: "testuser",
		Email:    "test@example.com",
		Role:     domain.RoleUser,
	}
	require.NoError(t, userRepo.addUser(user, "password123"))

	clientSecret := "client-secret"
	secretHash, _ := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	secretHashStr := string(secretHash)
	oauthRepo.addClient(&port.OAuthClient{
		ClientID:         "test-client",
		ClientSecretHash: &secretHashStr,
		IsConfidential:   true,
		GrantTypes:       []string{"password"},
	})

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("username", "testuser")
	form.Set("password", "password123")

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("test-client", clientSecret)

	rec := httptest.NewRecorder()
	h.OAuthToken(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "access_token")
	assert.Contains(t, body, "refresh_token")
	assert.Contains(t, body, "bearer")

	assert.Equal(t, 1, len(authRepo.refreshTokens))
	assert.Equal(t, 1, len(authRepo.sessions))
}

func TestOAuthToken_PasswordGrant_InvalidCredentials(t *testing.T) {
	h, userRepo, _, oauthRepo := setupOAuthTest(t)

	userID := uuid.NewString()
	user := &domain.User{
		ID:       userID,
		Username: "testuser",
		Email:    "test@example.com",
		Role:     domain.RoleUser,
	}
	require.NoError(t, userRepo.addUser(user, "password123"))

	clientSecret := "client-secret"
	secretHash, _ := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	secretHashStr := string(secretHash)
	oauthRepo.addClient(&port.OAuthClient{
		ClientID:         "test-client",
		ClientSecretHash: &secretHashStr,
		IsConfidential:   true,
		GrantTypes:       []string{"password"},
	})

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("username", "testuser")
	form.Set("password", "wrongpassword")

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("test-client", clientSecret)

	rec := httptest.NewRecorder()
	h.OAuthToken(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_grant")
}

func TestOAuthToken_PasswordGrant_MissingUsername(t *testing.T) {
	h, _, _, oauthRepo := setupOAuthTest(t)

	clientSecret := "client-secret"
	secretHash, _ := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	secretHashStr := string(secretHash)
	oauthRepo.addClient(&port.OAuthClient{
		ClientID:         "test-client",
		ClientSecretHash: &secretHashStr,
		IsConfidential:   true,
		GrantTypes:       []string{"password"},
	})

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("password", "password123")

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("test-client", clientSecret)

	rec := httptest.NewRecorder()
	h.OAuthToken(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_request")
}

func TestOAuthToken_PasswordGrant_UserNotFound(t *testing.T) {
	h, _, _, oauthRepo := setupOAuthTest(t)

	clientSecret := "client-secret"
	secretHash, _ := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	secretHashStr := string(secretHash)
	oauthRepo.addClient(&port.OAuthClient{
		ClientID:         "test-client",
		ClientSecretHash: &secretHashStr,
		IsConfidential:   true,
		GrantTypes:       []string{"password"},
	})

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("username", "nonexistent@example.com")
	form.Set("password", "password123")

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("test-client", clientSecret)

	rec := httptest.NewRecorder()
	h.OAuthToken(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_grant")
}

func TestOAuthToken_RefreshGrant_Success(t *testing.T) {
	h, userRepo, authRepo, oauthRepo := setupOAuthTest(t)

	userID := uuid.NewString()
	user := &domain.User{
		ID:       userID,
		Username: "testuser",
		Email:    "test@example.com",
		Role:     domain.RoleAdmin,
	}
	require.NoError(t, userRepo.addUser(user, "password123"))

	clientSecret := "client-secret"
	secretHash, _ := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	secretHashStr := string(secretHash)
	oauthRepo.addClient(&port.OAuthClient{
		ClientID:         "test-client",
		ClientSecretHash: &secretHashStr,
		IsConfidential:   true,
		GrantTypes:       []string{"refresh_token"},
	})

	refreshToken := "existing-refresh-token"
	authRepo.CreateRefreshToken(context.Background(), &port.RefreshToken{
		ID:        uuid.NewString(),
		UserID:    userID,
		Token:     refreshToken,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	})

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("test-client", clientSecret)

	rec := httptest.NewRecorder()
	h.OAuthToken(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "access_token")
	assert.Contains(t, rec.Body.String(), "refresh_token")

	_, err := authRepo.GetRefreshToken(context.Background(), refreshToken)
	assert.Error(t, err)

	assert.Equal(t, 1, len(authRepo.refreshTokens))
}

func TestOAuthToken_RefreshGrant_InvalidToken(t *testing.T) {
	h, _, _, oauthRepo := setupOAuthTest(t)

	clientSecret := "client-secret"
	secretHash, _ := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	secretHashStr := string(secretHash)
	oauthRepo.addClient(&port.OAuthClient{
		ClientID:         "test-client",
		ClientSecretHash: &secretHashStr,
		IsConfidential:   true,
		GrantTypes:       []string{"refresh_token"},
	})

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", "invalid-token")

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("test-client", clientSecret)

	rec := httptest.NewRecorder()
	h.OAuthToken(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_grant")
}

func TestOAuthToken_RefreshGrant_MissingToken(t *testing.T) {
	h, _, _, oauthRepo := setupOAuthTest(t)

	clientSecret := "client-secret"
	secretHash, _ := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	secretHashStr := string(secretHash)
	oauthRepo.addClient(&port.OAuthClient{
		ClientID:         "test-client",
		ClientSecretHash: &secretHashStr,
		IsConfidential:   true,
		GrantTypes:       []string{"refresh_token"},
	})

	form := url.Values{}
	form.Set("grant_type", "refresh_token")

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("test-client", clientSecret)

	rec := httptest.NewRecorder()
	h.OAuthToken(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_request")
}

func TestOAuthToken_NoClientAuth(t *testing.T) {
	h, _, _, _ := setupOAuthTest(t)

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("username", "testuser")
	form.Set("password", "password123")

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	h.OAuthToken(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_client")
}

func TestOAuthToken_UnknownClient(t *testing.T) {
	h, _, _, _ := setupOAuthTest(t)

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("username", "testuser")
	form.Set("password", "password123")

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("unknown-client", "secret")

	rec := httptest.NewRecorder()
	h.OAuthToken(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_client")
}

func TestOAuthToken_InvalidClientSecret(t *testing.T) {
	h, _, _, oauthRepo := setupOAuthTest(t)

	clientSecret := "client-secret"
	secretHash, _ := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	secretHashStr := string(secretHash)
	oauthRepo.addClient(&port.OAuthClient{
		ClientID:         "test-client",
		ClientSecretHash: &secretHashStr,
		IsConfidential:   true,
		GrantTypes:       []string{"password"},
	})

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("username", "testuser")
	form.Set("password", "password123")

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("test-client", "wrong-secret")

	rec := httptest.NewRecorder()
	h.OAuthToken(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_client")
}

func TestOAuthToken_GrantTypeNotAllowed(t *testing.T) {
	h, _, _, oauthRepo := setupOAuthTest(t)

	clientSecret := "client-secret"
	secretHash, _ := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	secretHashStr := string(secretHash)
	oauthRepo.addClient(&port.OAuthClient{
		ClientID:         "test-client",
		ClientSecretHash: &secretHashStr,
		IsConfidential:   true,
		GrantTypes:       []string{"refresh_token"},
	})

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("username", "testuser")
	form.Set("password", "password123")

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("test-client", clientSecret)

	rec := httptest.NewRecorder()
	h.OAuthToken(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "unsupported_grant_type")
}

func TestOAuthToken_MissingGrantType(t *testing.T) {
	h, _, _, oauthRepo := setupOAuthTest(t)

	clientSecret := "client-secret"
	secretHash, _ := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	secretHashStr := string(secretHash)
	oauthRepo.addClient(&port.OAuthClient{
		ClientID:         "test-client",
		ClientSecretHash: &secretHashStr,
		IsConfidential:   true,
		GrantTypes:       []string{"password"},
	})

	form := url.Values{}
	form.Set("username", "testuser")
	form.Set("password", "password123")

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("test-client", clientSecret)

	rec := httptest.NewRecorder()
	h.OAuthToken(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_request")
}

func TestOAuthToken_UnsupportedGrantType(t *testing.T) {
	h, _, _, oauthRepo := setupOAuthTest(t)

	clientSecret := "client-secret"
	secretHash, _ := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	secretHashStr := string(secretHash)
	oauthRepo.addClient(&port.OAuthClient{
		ClientID:         "test-client",
		ClientSecretHash: &secretHashStr,
		IsConfidential:   true,
		GrantTypes:       []string{"client_credentials"},
	})

	form := url.Values{}
	form.Set("grant_type", "client_credentials")

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("test-client", clientSecret)

	rec := httptest.NewRecorder()
	h.OAuthToken(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "unsupported_grant_type")
}

func TestOAuthToken_InvalidFormData(t *testing.T) {
	h, _, _, _ := setupOAuthTest(t)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader("invalid%form%data"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("test-client", "secret")

	rec := httptest.NewRecorder()
	h.OAuthToken(rec, req)

	assert.NotEqual(t, http.StatusOK, rec.Code)
}
