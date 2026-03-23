package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"vidra-core/internal/domain"
	"vidra-core/internal/email"
	"vidra-core/internal/middleware"
	"vidra-core/internal/repository"
	"vidra-core/internal/testutil"
	"vidra-core/internal/usecase"
)

// TestUnverifiedUserRestrictions tests that unverified users cannot perform protected actions
func TestUnverifiedUserRestrictions(t *testing.T) {
	// Skip if not integration test
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	// Setup test database
	db := testutil.SetupTestDBWithMigration(t)
	defer db.Close()

	// Run migrations
	testutil.RunMigrations(t, db)

	// Create repositories
	userRepo := repository.NewUserRepository(db)
	_ = repository.NewEmailVerificationRepository(db)

	// Create unverified user
	ctx := context.Background()
	userID := uuid.NewString()
	passwordHash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)

	unverifiedUser := &domain.User{
		ID:            userID,
		Username:      "unverified",
		Email:         "unverified@example.com",
		DisplayName:   "Unverified User",
		Role:          domain.RoleUser,
		IsActive:      true,
		EmailVerified: false,
		CreatedAt:     testutil.Now(),
		UpdatedAt:     testutil.Now(),
	}

	err := userRepo.Create(ctx, unverifiedUser, string(passwordHash))
	require.NoError(t, err)

	// Create verified user for comparison
	verifiedUserID := uuid.NewString()
	verifiedUser := &domain.User{
		ID:            verifiedUserID,
		Username:      "verified",
		Email:         "verified@example.com",
		DisplayName:   "Verified User",
		Role:          domain.RoleUser,
		IsActive:      true,
		EmailVerified: true,
		CreatedAt:     testutil.Now(),
		UpdatedAt:     testutil.Now(),
	}

	err = userRepo.Create(ctx, verifiedUser, string(passwordHash))
	require.NoError(t, err)
	err = userRepo.MarkEmailAsVerified(ctx, verifiedUserID)
	require.NoError(t, err)

	// Create router with email verification middleware
	router := chi.NewRouter()

	// Protected endpoints that require email verification
	router.Group(func(r chi.Router) {
		r.Use(middleware.RequireEmailVerification(userRepo))

		// Video upload endpoint
		r.Post("/api/v1/videos", func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"message": "Video uploaded"})
		})

		// Comment endpoint
		r.Post("/api/v1/videos/{id}/comments", func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"message": "Comment posted"})
		})

		// Subscribe endpoint
		r.Post("/api/v1/users/{id}/subscribe", func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"message": "Subscribed"})
		})
	})

	// Test cases
	tests := []struct {
		name           string
		endpoint       string
		method         string
		userID         string
		emailVerified  bool
		expectedStatus int
	}{
		{
			name:           "Unverified user cannot upload video",
			endpoint:       "/api/v1/videos",
			method:         http.MethodPost,
			userID:         userID,
			emailVerified:  false,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "Verified user can upload video",
			endpoint:       "/api/v1/videos",
			method:         http.MethodPost,
			userID:         verifiedUserID,
			emailVerified:  true,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Unverified user cannot comment",
			endpoint:       "/api/v1/videos/123/comments",
			method:         http.MethodPost,
			userID:         userID,
			emailVerified:  false,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "Verified user can comment",
			endpoint:       "/api/v1/videos/123/comments",
			method:         http.MethodPost,
			userID:         verifiedUserID,
			emailVerified:  true,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Unverified user cannot subscribe",
			endpoint:       "/api/v1/users/456/subscribe",
			method:         http.MethodPost,
			userID:         userID,
			emailVerified:  false,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "Verified user can subscribe",
			endpoint:       "/api/v1/users/456/subscribe",
			method:         http.MethodPost,
			userID:         verifiedUserID,
			emailVerified:  true,
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create request
			body := bytes.NewReader([]byte(`{}`))
			req := httptest.NewRequest(tt.method, tt.endpoint, body)
			req.Header.Set("Content-Type", "application/json")

			// Add user context (simulating authenticated request)
			ctx := context.WithValue(req.Context(), middleware.UserIDKey, tt.userID)
			req = req.WithContext(ctx)

			// Record response
			rr := httptest.NewRecorder()

			// Serve request
			router.ServeHTTP(rr, req)

			// Check status
			assert.Equal(t, tt.expectedStatus, rr.Code)

			// If forbidden, check error message
			if tt.expectedStatus == http.StatusForbidden {
				var response map[string]interface{}
				err := json.NewDecoder(rr.Body).Decode(&response)
				require.NoError(t, err)

				errorInfo, ok := response["error"].(map[string]interface{})
				require.True(t, ok)
				assert.Equal(t, "EMAIL_NOT_VERIFIED", errorInfo["code"])
			}
		})
	}
}

// TestEmailVerificationWorkflow tests the complete email verification workflow
func TestEmailVerificationWorkflow(t *testing.T) {
	// Skip if not integration test
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	// Setup test database
	db := testutil.SetupTestDBWithMigration(t)
	defer db.Close()

	// Run migrations
	testutil.RunMigrations(t, db)

	// Create repositories
	userRepo := repository.NewUserRepository(db)
	verifyRepo := repository.NewEmailVerificationRepository(db)

	// Create email service (mock)
	emailConfig := &email.Config{
		SMTPHost:    "localhost",
		SMTPPort:    1025, // MailHog port
		FromAddress: "noreply@example.com",
		FromName:    "Test App",
		BaseURL:     "http://localhost:3000",
	}
	emailService := email.NewService(emailConfig)

	// Create verification service
	verificationService := usecase.NewEmailVerificationService(userRepo, verifyRepo, emailService)

	// Step 1: Register new user
	ctx := context.Background()
	userID := uuid.NewString()
	passwordHash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)

	newUser := &domain.User{
		ID:            userID,
		Username:      "newuser",
		Email:         "newuser@example.com",
		DisplayName:   "New User",
		Role:          domain.RoleUser,
		IsActive:      true,
		EmailVerified: false,
		CreatedAt:     testutil.Now(),
		UpdatedAt:     testutil.Now(),
	}

	err := userRepo.Create(ctx, newUser, string(passwordHash))
	require.NoError(t, err)

	// Step 2: Send verification email
	err = verificationService.SendVerificationEmail(ctx, userID)
	// May fail if SMTP not configured, but token should be created
	if err != nil {
		t.Logf("Email send failed (expected in test): %v", err)
	}

	// Step 3: Get the verification token
	token, err := verifyRepo.GetLatestTokenForUser(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, token)

	// Step 4: Verify the email using token
	err = verificationService.VerifyEmailWithToken(ctx, token.Token)
	require.NoError(t, err)

	// Step 5: Check user is now verified
	verifiedUser, err := userRepo.GetByID(ctx, userID)
	require.NoError(t, err)
	assert.True(t, verifiedUser.EmailVerified)
	assert.NotNil(t, verifiedUser.EmailVerifiedAt)

	// Step 6: Try to verify again (should fail)
	err = verificationService.SendVerificationEmail(ctx, userID)
	assert.Equal(t, domain.ErrEmailAlreadyVerified, err)
}

// TestResendVerificationLimits tests rate limiting for resend requests
func TestResendVerificationLimits(t *testing.T) {
	// Skip if not integration test
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	// Setup test database
	db := testutil.SetupTestDBWithMigration(t)
	defer db.Close()

	// Run migrations
	testutil.RunMigrations(t, db)

	// Create repositories
	userRepo := repository.NewUserRepository(db)
	verifyRepo := repository.NewEmailVerificationRepository(db)

	// Create mock email service
	emailConfig := &email.Config{
		SMTPHost:    "localhost",
		SMTPPort:    1025,
		FromAddress: "noreply@example.com",
		FromName:    "Test App",
		BaseURL:     "http://localhost:3000",
	}
	emailService := email.NewService(emailConfig)

	// Create verification service
	verificationService := usecase.NewEmailVerificationService(userRepo, verifyRepo, emailService)

	// Create unverified user
	ctx := context.Background()
	userID := uuid.NewString()
	passwordHash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)

	user := &domain.User{
		ID:            userID,
		Username:      "ratelimited",
		Email:         "ratelimited@example.com",
		DisplayName:   "Rate Limited User",
		Role:          domain.RoleUser,
		IsActive:      true,
		EmailVerified: false,
		CreatedAt:     testutil.Now(),
		UpdatedAt:     testutil.Now(),
	}

	err := userRepo.Create(ctx, user, string(passwordHash))
	require.NoError(t, err)

	// First request should succeed
	err = verificationService.ResendVerificationEmail(ctx, user.Email)
	// May fail if SMTP not configured
	if err != nil && err != domain.ErrEmailAlreadyVerified {
		t.Logf("First email send: %v", err)
	}

	// Immediate second request should reuse existing token (within 5 minutes)
	err = verificationService.ResendVerificationEmail(ctx, user.Email)
	if err != nil && err != domain.ErrEmailAlreadyVerified {
		t.Logf("Second email send: %v", err)
	}

	// Check that only one token was created
	tokens, err := getAllUserTokens(db, userID)
	require.NoError(t, err)
	assert.Equal(t, 1, len(tokens), "Should reuse existing token within rate limit window")
}

// Helper function to get all tokens for a user
func getAllUserTokens(db *sqlx.DB, userID string) ([]*domain.EmailVerificationToken, error) {
	var tokens []*domain.EmailVerificationToken
	query := `
		SELECT id, user_id, token, code, expires_at, created_at, used_at
		FROM email_verification_tokens
		WHERE user_id = $1
		ORDER BY created_at DESC
	`
	err := db.Select(&tokens, query, userID)
	return tokens, err
}
