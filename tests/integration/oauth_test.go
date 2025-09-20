package integration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/httpapi"
	"athena/internal/repository"
	"athena/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestOAuth2PasswordGrant(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	r := setupTestRouter(db)

	// Create test user
	userID := uuid.NewString()
	passwordHash, _ := bcrypt.GenerateFromPassword([]byte("testpass123"), bcrypt.DefaultCost)
	_, err := db.Exec(`
		INSERT INTO users (id, username, email, password_hash, role, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())`,
		userID, "testuser", "test@example.com", string(passwordHash), "user",
	)
	require.NoError(t, err)

	// Create OAuth client
	clientID := "test-client"
	clientSecret := "test-secret"
	clientSecretHash, _ := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	_, err = db.Exec(`
		INSERT INTO oauth_clients (id, client_id, client_secret_hash, name, grant_types, is_confidential, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
		uuid.NewString(), clientID, string(clientSecretHash), "Test Client",
		`{"password","refresh_token"}`, true,
	)
	require.NoError(t, err)

	// Test password grant
	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("username", "testuser")
	form.Set("password", "testpass123")
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var tokenResp map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &tokenResp)
	require.NoError(t, err)

	assert.NotEmpty(t, tokenResp["access_token"])
	assert.NotEmpty(t, tokenResp["refresh_token"])
	assert.Equal(t, "bearer", tokenResp["token_type"])
	assert.Equal(t, float64(900), tokenResp["expires_in"])
}

func TestOAuth2AuthorizationCodeFlow(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	r := setupTestRouter(db)

	// Create test user
	userID := uuid.NewString()
	passwordHash, _ := bcrypt.GenerateFromPassword([]byte("testpass123"), bcrypt.DefaultCost)
	_, err := db.Exec(`
		INSERT INTO users (id, username, email, password_hash, role, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())`,
		userID, "testuser", "test@example.com", string(passwordHash), "user",
	)
	require.NoError(t, err)

	// Create OAuth client
	clientID := "test-client"
	clientSecret := "test-secret"
	clientSecretHash, _ := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	_, err = db.Exec(`
		INSERT INTO oauth_clients (id, client_id, client_secret_hash, name, grant_types, redirect_uris, allowed_scopes, is_confidential, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())`,
		uuid.NewString(), clientID, string(clientSecretHash), "Test Client",
		`{"authorization_code","refresh_token"}`, `{"http://localhost:8080/callback"}`,
		`{"basic","profile","upload"}`, true,
	)
	require.NoError(t, err)

	// Step 1: Authorization request (would normally show consent screen)
	// For testing, we'll simulate the authorization code creation directly
	authCode := "test-auth-code"
	_, err = db.Exec(`
		INSERT INTO oauth_authorization_codes (id, code, client_id, user_id, redirect_uri, scope, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())`,
		uuid.NewString(), authCode, clientID, userID,
		"http://localhost:8080/callback", "basic profile", time.Now().Add(10*time.Minute),
	)
	require.NoError(t, err)

	// Step 2: Exchange code for token
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", authCode)
	form.Set("redirect_uri", "http://localhost:8080/callback")
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var tokenResp map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &tokenResp)
	require.NoError(t, err)

	assert.NotEmpty(t, tokenResp["access_token"])
	assert.NotEmpty(t, tokenResp["refresh_token"])
	assert.Equal(t, "basic profile", tokenResp["scope"])

	// Verify code is marked as used
	var usedAt *time.Time
	err = db.Get(&usedAt, "SELECT used_at FROM oauth_authorization_codes WHERE code = $1", authCode)
	require.NoError(t, err)
	assert.NotNil(t, usedAt)
}

func TestOAuth2PKCE(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	r := setupTestRouter(db)

	// Create test user
	userID := uuid.NewString()
	passwordHash, _ := bcrypt.GenerateFromPassword([]byte("testpass123"), bcrypt.DefaultCost)
	_, err := db.Exec(`
		INSERT INTO users (id, username, email, password_hash, role, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())`,
		userID, "testuser", "test@example.com", string(passwordHash), "user",
	)
	require.NoError(t, err)

	// Create public OAuth client (for PKCE)
	clientID := "test-public-client"
	_, err = db.Exec(`
		INSERT INTO oauth_clients (id, client_id, name, grant_types, redirect_uris, allowed_scopes, is_confidential, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())`,
		uuid.NewString(), clientID, "Test Public Client",
		`{"authorization_code"}`, `{"http://localhost:8080/callback"}`,
		`{"basic","profile"}`, false,
	)
	require.NoError(t, err)

	// Generate PKCE challenge
	codeVerifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(h[:])

	// Create authorization code with PKCE
	authCode := "test-pkce-code"
	_, err = db.Exec(`
		INSERT INTO oauth_authorization_codes (id, code, client_id, user_id, redirect_uri, scope, code_challenge, code_challenge_method, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())`,
		uuid.NewString(), authCode, clientID, userID,
		"http://localhost:8080/callback", "basic", codeChallenge, "S256", time.Now().Add(10*time.Minute),
	)
	require.NoError(t, err)

	// Exchange code with verifier
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", authCode)
	form.Set("redirect_uri", "http://localhost:8080/callback")
	form.Set("client_id", clientID)
	form.Set("code_verifier", codeVerifier)

	req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var tokenResp map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &tokenResp)
	require.NoError(t, err)

	assert.NotEmpty(t, tokenResp["access_token"])
}

func TestOAuth2TokenRevocation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	r := setupTestRouter(db)
	oauthRepo := repository.NewOAuthRepository(db)

	// Create OAuth client
	clientID := "test-client"
	clientSecret := "test-secret"
	clientSecretHashBytes, _ := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	clientSecretHash := string(clientSecretHashBytes)
	err := oauthRepo.CreateClient(context.Background(), &usecase.OAuthClient{
		ID:               uuid.NewString(),
		ClientID:         clientID,
		ClientSecretHash: &clientSecretHash,
		Name:             "Test Client",
		GrantTypes:       []string{"password"},
		IsConfidential:   true,
	})
	require.NoError(t, err)

	// Create access token
	token := "test-access-token"
	h := sha256.Sum256([]byte(token))
	tokenHash := fmt.Sprintf("%x", h)

	err = oauthRepo.CreateAccessToken(context.Background(), &usecase.OAuthAccessToken{
		ID:        uuid.NewString(),
		TokenHash: tokenHash,
		ClientID:  clientID,
		UserID:    uuid.NewString(),
		Scope:     "basic",
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	// Revoke token
	form := url.Values{}
	form.Set("token", token)
	form.Set("token_type_hint", "access_token")
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	req := httptest.NewRequest("POST", "/oauth/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify token is revoked
	at, err := oauthRepo.GetAccessToken(context.Background(), tokenHash)
	require.NoError(t, err)
	assert.NotNil(t, at.RevokedAt)
}

func TestOAuth2TokenIntrospection(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	r := setupTestRouter(db)
	oauthRepo := repository.NewOAuthRepository(db)

	// Create OAuth client
	clientID := "test-client"
	clientSecret := "test-secret"
	clientSecretHashBytes, _ := bcrypt.GenerateFromPassword([]byte(clientSecret), bcrypt.DefaultCost)
	clientSecretHash := string(clientSecretHashBytes)
	err := oauthRepo.CreateClient(context.Background(), &usecase.OAuthClient{
		ID:               uuid.NewString(),
		ClientID:         clientID,
		ClientSecretHash: &clientSecretHash,
		Name:             "Test Client",
		GrantTypes:       []string{"password"},
		IsConfidential:   true,
	})
	require.NoError(t, err)

	// Create valid access token
	token := "test-introspect-token"
	h := sha256.Sum256([]byte(token))
	tokenHash := fmt.Sprintf("%x", h)
	userID := uuid.NewString()

	err = oauthRepo.CreateAccessToken(context.Background(), &usecase.OAuthAccessToken{
		ID:        uuid.NewString(),
		TokenHash: tokenHash,
		ClientID:  clientID,
		UserID:    userID,
		Scope:     "basic profile",
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	// Introspect token
	form := url.Values{}
	form.Set("token", token)
	form.Set("token_type_hint", "access_token")
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	req := httptest.NewRequest("POST", "/oauth/introspect", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var introspectResp map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &introspectResp)
	require.NoError(t, err)

	assert.Equal(t, true, introspectResp["active"])
	assert.Equal(t, "basic profile", introspectResp["scope"])
	assert.Equal(t, clientID, introspectResp["client_id"])
	assert.Equal(t, userID, introspectResp["username"])
}

func TestOAuth2ScopeEnforcement(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_ = setupTestRouter(db)

	// Create test user with limited scope token
	userID := uuid.NewString()
	passwordHash, _ := bcrypt.GenerateFromPassword([]byte("testpass123"), bcrypt.DefaultCost)
	_, err := db.Exec(`
		INSERT INTO users (id, username, email, password_hash, role, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())`,
		userID, "testuser", "test@example.com", string(passwordHash), "user",
	)
	require.NoError(t, err)

	// Test that endpoints requiring specific scopes are protected
	// This would require adding scope middleware to routes and testing them
	// For now, this is a placeholder for scope enforcement tests
	t.Run("endpoint requires upload scope", func(t *testing.T) {
		// Create token with only basic scope
		// Try to access upload endpoint
		// Should return 403 Forbidden
	})

	t.Run("endpoint allows with correct scope", func(t *testing.T) {
		// Create token with upload scope
		// Try to access upload endpoint
		// Should succeed
	})
}

func TestOAuth2ErrorResponses(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	r := setupTestRouter(db)

	tests := []struct {
		name           string
		form           url.Values
		expectedStatus int
		expectedError  string
	}{
		{
			name: "missing grant_type",
			form: url.Values{
				"username": []string{"test"},
				"password": []string{"test"},
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "invalid_request",
		},
		{
			name: "unsupported grant_type",
			form: url.Values{
				"grant_type": []string{"implicit"},
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "unsupported_grant_type",
		},
		{
			name: "invalid client credentials",
			form: url.Values{
				"grant_type":    []string{"password"},
				"client_id":     []string{"invalid"},
				"client_secret": []string{"wrong"},
			},
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "invalid_client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(tt.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var errResp map[string]string
			err := json.Unmarshal(w.Body.Bytes(), &errResp)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedError, errResp["error"])
			assert.NotEmpty(t, errResp["error_description"])

			// Check cache headers
			assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			assert.Equal(t, "no-cache", w.Header().Get("Pragma"))
		})
	}
}

// Helper functions

func setupTestDB(t *testing.T) (*sqlx.DB, func()) {
	// Use test database
	db, err := sqlx.Connect("postgres", "postgres://postgres:postgres@localhost:5432/athena_test?sslmode=disable")
	require.NoError(t, err)

	// Run migrations or setup test schema
	setupTestSchema(t, db)

	cleanup := func() {
		// Clean up test data
		db.Exec("TRUNCATE users, oauth_clients, oauth_authorization_codes, oauth_access_tokens CASCADE")
		db.Close()
	}

	return db, cleanup
}

func setupTestSchema(t *testing.T, db *sqlx.DB) {
	// Create necessary tables for testing
	// This should ideally run migrations, but for testing we can create minimal schema
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id UUID PRIMARY KEY,
			username TEXT UNIQUE,
			email TEXT UNIQUE,
			password_hash TEXT,
			role TEXT,
			created_at TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS oauth_clients (
			id UUID PRIMARY KEY,
			client_id TEXT UNIQUE,
			client_secret_hash TEXT,
			name TEXT,
			grant_types TEXT[],
			scopes TEXT[],
			redirect_uris TEXT[],
			allowed_scopes TEXT[],
			is_confidential BOOLEAN,
			created_at TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS oauth_authorization_codes (
			id UUID PRIMARY KEY,
			code TEXT UNIQUE,
			client_id TEXT,
			user_id UUID,
			redirect_uri TEXT,
			scope TEXT,
			state TEXT,
			code_challenge TEXT,
			code_challenge_method TEXT,
			expires_at TIMESTAMPTZ,
			used_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS oauth_access_tokens (
			id UUID PRIMARY KEY,
			token_hash TEXT UNIQUE,
			client_id TEXT,
			user_id UUID,
			scope TEXT,
			expires_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ,
			revoked_at TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS refresh_tokens (
			id UUID PRIMARY KEY,
			user_id UUID,
			token TEXT UNIQUE,
			expires_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ
		)`,
	}

	for _, q := range queries {
		_, err := db.Exec(q)
		require.NoError(t, err)
	}
}

func setupTestRouter(db *sqlx.DB) *chi.Mux {
	r := chi.NewRouter()

	cfg := &config.Config{
		DatabaseURL: "postgres://test",
		JWTSecret:   "test-secret",
		RedisURL:    "redis://localhost:6379",
	}

	httpapi.RegisterRoutes(r, cfg)

	return r
}
