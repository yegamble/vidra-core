//go:build integration

package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"athena/internal/domain"
	"athena/internal/generated"
	"athena/internal/httpapi"
	"athena/internal/httpapi/handlers/auth"
	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/usecase"
	"athena/tests/integration"
)

func TestTwoFASetupFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup test environment
	cleanup, testDB, redisClient, cfg := integration.SetupTestEnvironment(t)
	defer cleanup()

	// Initialize repositories
	userRepo := repository.NewUserRepository(testDB)
	backupCodeRepo := repository.NewTwoFABackupCodeRepository(testDB)
	authRepo := repository.NewAuthComposite(testDB, redisClient)

	// Initialize services
	twoFAService := usecase.NewTwoFAService(userRepo, backupCodeRepo, "TestApp")

	// Create handlers
	handlers := auth.NewTwoFAHandlers(twoFAService)

	// Create a test user
	ctx := context.Background()
	testUser := &domain.User{
		ID:        uuid.NewString(),
		Username:  "testuser",
		Email:     "test@example.com",
		Role:      domain.RoleUser,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	require.NoError(t, err)

	err = userRepo.Create(ctx, testUser, string(passwordHash))
	require.NoError(t, err)

	// Generate JWT token for the user
	server := httpapi.NewServer(userRepo, authRepo, cfg.JWTSecret, redisClient, 5*time.Second, "", "", 5*time.Second, cfg)
	token := server.GenerateJWTForTests(testUser.ID, string(testUser.Role), 15*time.Minute)

	t.Run("Complete 2FA Setup Flow", func(t *testing.T) {
		// Step 1: Initiate 2FA setup
		req := httptest.NewRequest(http.MethodPost, "/auth/2fa/setup", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, testUser.ID))

		w := httptest.NewRecorder()
		handlers.SetupTwoFA(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var setupResp domain.TwoFASetupResponse
		err := json.NewDecoder(w.Body).Decode(&setupResp)
		require.NoError(t, err)

		assert.NotEmpty(t, setupResp.Secret)
		assert.NotEmpty(t, setupResp.QRCodeURI)
		assert.Len(t, setupResp.BackupCodes, 10)

		// Step 2: Generate a valid TOTP code
		code, err := totp.GenerateCode(setupResp.Secret, time.Now())
		require.NoError(t, err)

		// Step 3: Verify setup with the code
		verifyReq := domain.TwoFAVerifySetupRequest{
			Code: code,
		}
		verifyReqBody, err := json.Marshal(verifyReq)
		require.NoError(t, err)

		req = httptest.NewRequest(http.MethodPost, "/auth/2fa/verify-setup", bytes.NewReader(verifyReqBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, testUser.ID))

		w = httptest.NewRecorder()
		handlers.VerifyTwoFASetup(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var verifyResp domain.TwoFAVerifySetupResponse
		err = json.NewDecoder(w.Body).Decode(&verifyResp)
		require.NoError(t, err)
		assert.True(t, verifyResp.Enabled)

		// Verify that 2FA is now enabled in the database
		updatedUser, err := userRepo.GetByID(ctx, testUser.ID)
		require.NoError(t, err)
		assert.True(t, updatedUser.TwoFAEnabled)
	})

	t.Run("Setup with wrong code should fail", func(t *testing.T) {
		// Create another test user
		testUser2 := &domain.User{
			ID:        uuid.NewString(),
			Username:  "testuser2",
			Email:     "test2@example.com",
			Role:      domain.RoleUser,
			IsActive:  true,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		err := userRepo.Create(ctx, testUser2, string(passwordHash))
		require.NoError(t, err)

		token2 := server.GenerateJWTForTests(testUser2.ID, string(testUser2.Role), 15*time.Minute)

		// Step 1: Initiate 2FA setup
		req := httptest.NewRequest(http.MethodPost, "/auth/2fa/setup", nil)
		req.Header.Set("Authorization", "Bearer "+token2)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, testUser2.ID))

		w := httptest.NewRecorder()
		handlers.SetupTwoFA(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		// Step 2: Try to verify with wrong code
		verifyReq := domain.TwoFAVerifySetupRequest{
			Code: "000000", // Invalid code
		}
		verifyReqBody, err := json.Marshal(verifyReq)
		require.NoError(t, err)

		req = httptest.NewRequest(http.MethodPost, "/auth/2fa/verify-setup", bytes.NewReader(verifyReqBody))
		req.Header.Set("Authorization", "Bearer "+token2)
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, testUser2.ID))

		w = httptest.NewRecorder()
		handlers.VerifyTwoFASetup(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		// Verify that 2FA is still not enabled
		updatedUser, err := userRepo.GetByID(ctx, testUser2.ID)
		require.NoError(t, err)
		assert.False(t, updatedUser.TwoFAEnabled)
	})
}

func TestTwoFALoginFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup test environment
	cleanup, testDB, redisClient, cfg := integration.SetupTestEnvironment(t)
	defer cleanup()

	// Initialize repositories
	userRepo := repository.NewUserRepository(testDB)
	backupCodeRepo := repository.NewTwoFABackupCodeRepository(testDB)
	authRepo := repository.NewAuthComposite(testDB, redisClient)

	// Initialize services
	twoFAService := usecase.NewTwoFAService(userRepo, backupCodeRepo, "TestApp")

	// Create server with 2FA service
	server := httpapi.NewServer(userRepo, authRepo, cfg.JWTSecret, redisClient, 5*time.Second, "", "", 5*time.Second, cfg)
	server.SetTwoFAService(twoFAService)

	// Create a test user
	ctx := context.Background()
	testUser := &domain.User{
		ID:        uuid.NewString(),
		Username:  "testuser",
		Email:     "test@example.com",
		Role:      domain.RoleUser,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	require.NoError(t, err)

	err = userRepo.Create(ctx, testUser, string(passwordHash))
	require.NoError(t, err)

	t.Run("Login without 2FA should succeed", func(t *testing.T) {
		loginReq := map[string]string{
			"email":    testUser.Email,
			"password": "password123",
		}
		loginReqBody, err := json.Marshal(loginReq)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(loginReqBody))
		req.Header.Set("Content-Type", "application/json")

		w := httptest.NewRecorder()
		server.Login(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var authResp generated.AuthResponse
		err = json.NewDecoder(w.Body).Decode(&authResp)
		require.NoError(t, err)
		assert.NotEmpty(t, authResp.AccessToken)
	})

	// Enable 2FA for the user
	secret := "JBSWY3DPEHPK3PXP"
	testUser.TwoFAEnabled = true
	testUser.TwoFASecret = secret
	testUser.TwoFAConfirmedAt.Valid = true
	testUser.TwoFAConfirmedAt.Time = time.Now()
	err = userRepo.Update(ctx, testUser)
	require.NoError(t, err)

	t.Run("Login with 2FA but no code should fail", func(t *testing.T) {
		loginReq := map[string]string{
			"email":    testUser.Email,
			"password": "password123",
		}
		loginReqBody, err := json.Marshal(loginReq)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(loginReqBody))
		req.Header.Set("Content-Type", "application/json")

		w := httptest.NewRecorder()
		server.Login(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("Login with 2FA and wrong code should fail", func(t *testing.T) {
		loginReq := map[string]interface{}{
			"email":      testUser.Email,
			"password":   "password123",
			"twofa_code": "000000",
		}
		loginReqBody, err := json.Marshal(loginReq)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(loginReqBody))
		req.Header.Set("Content-Type", "application/json")

		w := httptest.NewRecorder()
		server.Login(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("Login with 2FA and correct code should succeed", func(t *testing.T) {
		code, err := totp.GenerateCode(secret, time.Now())
		require.NoError(t, err)

		loginReq := map[string]interface{}{
			"email":      testUser.Email,
			"password":   "password123",
			"twofa_code": code,
		}
		loginReqBody, err := json.Marshal(loginReq)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(loginReqBody))
		req.Header.Set("Content-Type", "application/json")

		w := httptest.NewRecorder()
		server.Login(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var authResp generated.AuthResponse
		err = json.NewDecoder(w.Body).Decode(&authResp)
		require.NoError(t, err)
		assert.NotEmpty(t, authResp.AccessToken)
	})
}

func TestTwoFADisableFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup test environment
	cleanup, testDB, redisClient, cfg := integration.SetupTestEnvironment(t)
	defer cleanup()

	// Initialize repositories
	userRepo := repository.NewUserRepository(testDB)
	backupCodeRepo := repository.NewTwoFABackupCodeRepository(testDB)
	authRepo := repository.NewAuthComposite(testDB, redisClient)

	// Initialize services
	twoFAService := usecase.NewTwoFAService(userRepo, backupCodeRepo, "TestApp")

	// Create handlers
	handlers := auth.NewTwoFAHandlers(twoFAService)

	// Create a test user with 2FA enabled
	ctx := context.Background()
	secret := "JBSWY3DPEHPK3PXP"
	testUser := &domain.User{
		ID:           uuid.NewString(),
		Username:     "testuser",
		Email:        "test@example.com",
		Role:         domain.RoleUser,
		IsActive:     true,
		TwoFAEnabled: true,
		TwoFASecret:  secret,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	testUser.TwoFAConfirmedAt.Valid = true
	testUser.TwoFAConfirmedAt.Time = time.Now()

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	require.NoError(t, err)

	err = userRepo.Create(ctx, testUser, string(passwordHash))
	require.NoError(t, err)

	// Generate JWT token for the user
	server := httpapi.NewServer(userRepo, authRepo, cfg.JWTSecret, redisClient, 5*time.Second, "", "", 5*time.Second, cfg)
	token := server.GenerateJWTForTests(testUser.ID, string(testUser.Role), 15*time.Minute)

	t.Run("Disable 2FA with valid credentials", func(t *testing.T) {
		code, err := totp.GenerateCode(secret, time.Now())
		require.NoError(t, err)

		disableReq := domain.TwoFADisableRequest{
			Password: "password123",
			Code:     code,
		}
		disableReqBody, err := json.Marshal(disableReq)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/auth/2fa/disable", bytes.NewReader(disableReqBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, testUser.ID))

		w := httptest.NewRecorder()
		handlers.DisableTwoFA(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var disableResp domain.TwoFADisableResponse
		err = json.NewDecoder(w.Body).Decode(&disableResp)
		require.NoError(t, err)
		assert.True(t, disableResp.Disabled)

		// Verify that 2FA is now disabled in the database
		updatedUser, err := userRepo.GetByID(ctx, testUser.ID)
		require.NoError(t, err)
		assert.False(t, updatedUser.TwoFAEnabled)
	})
}

func TestTwoFARegenerateBackupCodes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup test environment
	cleanup, testDB, redisClient, cfg := integration.SetupTestEnvironment(t)
	defer cleanup()

	// Initialize repositories
	userRepo := repository.NewUserRepository(testDB)
	backupCodeRepo := repository.NewTwoFABackupCodeRepository(testDB)
	authRepo := repository.NewAuthComposite(testDB, redisClient)

	// Initialize services
	twoFAService := usecase.NewTwoFAService(userRepo, backupCodeRepo, "TestApp")

	// Create handlers
	handlers := auth.NewTwoFAHandlers(twoFAService)

	// Create a test user with 2FA enabled
	ctx := context.Background()
	secret := "JBSWY3DPEHPK3PXP"
	testUser := &domain.User{
		ID:           uuid.NewString(),
		Username:     "testuser",
		Email:        "test@example.com",
		Role:         domain.RoleUser,
		IsActive:     true,
		TwoFAEnabled: true,
		TwoFASecret:  secret,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	testUser.TwoFAConfirmedAt.Valid = true
	testUser.TwoFAConfirmedAt.Time = time.Now()

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	require.NoError(t, err)

	err = userRepo.Create(ctx, testUser, string(passwordHash))
	require.NoError(t, err)

	// Generate JWT token for the user
	server := httpapi.NewServer(userRepo, authRepo, cfg.JWTSecret, redisClient, 5*time.Second, "", "", 5*time.Second, cfg)
	token := server.GenerateJWTForTests(testUser.ID, string(testUser.Role), 15*time.Minute)

	t.Run("Regenerate backup codes with valid code", func(t *testing.T) {
		code, err := totp.GenerateCode(secret, time.Now())
		require.NoError(t, err)

		regenReq := domain.TwoFARegenerateBackupCodesRequest{
			Code: code,
		}
		regenReqBody, err := json.Marshal(regenReq)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/auth/2fa/regenerate-backup-codes", bytes.NewReader(regenReqBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, testUser.ID))

		w := httptest.NewRecorder()
		handlers.RegenerateBackupCodes(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var regenResp domain.TwoFARegenerateBackupCodesResponse
		err = json.NewDecoder(w.Body).Decode(&regenResp)
		require.NoError(t, err)
		assert.Len(t, regenResp.BackupCodes, 10)
	})
}
