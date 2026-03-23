package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/usecase"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// Response is an alias for shared.Response for tests
type Response = shared.Response

// ErrorInfo is an alias for shared.ErrorInfo for tests
type ErrorInfo = shared.ErrorInfo

// Meta is an alias for shared.Meta for tests
type Meta = shared.Meta

// NewServer creates an AuthHandlers instance for tests (backwards compatibility)
func NewServer(
	userRepo interface{},
	authRepo interface{},
	jwtSecret string,
	emailService interface{},
	pingTimeout int,
	ipfsAPI string,
	ipfsClusterAPI string,
	redisTimeout int,
	redisClient interface{},
) *AuthHandlers {
	// This is a stub for test compatibility
	// Tests should be refactored to use NewAuthHandlers directly
	h := &AuthHandlers{
		jwtSecret:      jwtSecret,
		ipfsAPI:        ipfsAPI,
		ipfsClusterAPI: ipfsClusterAPI,
	}

	// Set the userRepo if provided
	if ur, ok := userRepo.(usecase.UserRepository); ok && ur != nil {
		h.userRepo = ur
	}

	// Set the authRepo if provided
	if ar, ok := authRepo.(usecase.AuthRepository); ok && ar != nil {
		h.authRepo = ar
	}

	return h
}

// integResp is an alias for testResponse for backwards compatibility
type integResp = testResponse

// testResponse is the standard response structure for tests
type testResponse struct {
	Data    json.RawMessage   `json:"data"`
	Error   *shared.ErrorInfo `json:"error"`
	Success bool              `json:"success"`
	Meta    *shared.Meta      `json:"meta"`
}

// withUserID adds a user ID to the context (test helper)
func withUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, middleware.UserIDKey, id)
}

// decodeResponse decodes a response for tests
func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder) testResponse {
	t.Helper()
	var resp testResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}

// Login implements legacy auth login for tests with real repository behavior.
func (h *AuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}
	if (req.Email == "" && req.Username == "") || req.Password == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_CREDENTIALS", "Email or username, and password are required"))
		return
	}
	if h.userRepo == nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "User repository not configured"))
		return
	}

	var user *domain.User
	var err error
	if req.Email != "" {
		user, err = h.userRepo.GetByEmail(r.Context(), req.Email)
	} else {
		user, err = h.userRepo.GetByUsername(r.Context(), req.Username)
	}
	if err != nil {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrInvalidCredentials)
		return
	}
	hash, err := h.userRepo.GetPasswordHash(r.Context(), user.ID)
	if err != nil {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrInvalidCredentials)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrInvalidCredentials)
		return
	}

	accessToken := h.generateJWTWithRole(user.ID, string(user.Role), 15*time.Minute)
	refreshToken := uuid.NewString()
	refreshExpires := time.Now().Add(7 * 24 * time.Hour)
	if h.authRepo != nil {
		rt := &usecase.RefreshToken{
			ID:        uuid.NewString(),
			UserID:    user.ID,
			Token:     refreshToken,
			ExpiresAt: refreshExpires,
			CreatedAt: time.Now(),
		}
		if err := h.authRepo.CreateRefreshToken(r.Context(), rt); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainErrorWithDetails("TOKEN_ISSUE_FAILED", "Failed to create refresh token", err.Error()))
			return
		}
		if err := h.authRepo.CreateSession(r.Context(), refreshToken, user.ID, refreshExpires); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("SESSION_CREATE_FAILED", "Failed to create session"))
			return
		}
	}

	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id":       user.ID,
			"username": user.Username,
			"email":    user.Email,
		},
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"expires_in":    int64(15 * 60),
	})
}

// Register implements legacy auth registration for tests with repository persistence.
func (h *AuthHandlers) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string  `json:"username"`
		Email       string  `json:"email"`
		Password    string  `json:"password"`
		DisplayName *string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	if req.Username == "" || req.Email == "" || req.Password == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FIELDS", "Username, email, and password are required"))
		return
	}
	if h.userRepo == nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "User repository not configured"))
		return
	}

	if _, err := h.userRepo.GetByEmail(r.Context(), req.Email); err == nil {
		shared.WriteError(w, http.StatusConflict, domain.NewDomainError("USER_EXISTS", "Email already in use"))
		return
	}
	if _, err := h.userRepo.GetByUsername(r.Context(), req.Username); err == nil {
		shared.WriteError(w, http.StatusConflict, domain.NewDomainError("USER_EXISTS", "Username already in use"))
		return
	}

	now := time.Now()
	displayName := ""
	if req.DisplayName != nil {
		displayName = *req.DisplayName
	}
	user := &domain.User{
		ID:          uuid.NewString(),
		Username:    req.Username,
		Email:       req.Email,
		DisplayName: displayName,
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to process password"))
		return
	}

	if err := h.userRepo.Create(r.Context(), user, string(hash)); err != nil {
		shared.WriteError(w, http.StatusConflict, domain.NewDomainError("CREATE_FAILED", "Failed to create user"))
		return
	}

	refreshToken := uuid.NewString()
	refreshExpires := time.Now().Add(7 * 24 * time.Hour)
	if h.authRepo != nil {
		rt := &usecase.RefreshToken{
			ID:        uuid.NewString(),
			UserID:    user.ID,
			Token:     refreshToken,
			ExpiresAt: refreshExpires,
			CreatedAt: time.Now(),
		}
		if err := h.authRepo.CreateRefreshToken(r.Context(), rt); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainErrorWithDetails("TOKEN_ISSUE_FAILED", "Failed to create refresh token", err.Error()))
			return
		}
		if err := h.authRepo.CreateSession(r.Context(), refreshToken, user.ID, refreshExpires); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("SESSION_CREATE_FAILED", "Failed to create session"))
			return
		}
	}

	w.Header().Set("Location", "/api/v1/users/"+user.ID)
	shared.WriteJSON(w, http.StatusCreated, map[string]any{
		"user": map[string]any{
			"id":       user.ID,
			"username": user.Username,
			"email":    user.Email,
		},
		"access_token":  h.generateJWTWithRole(user.ID, string(user.Role), 15*time.Minute),
		"refresh_token": refreshToken,
		"expires_in":    int64(15 * 60),
	})
}

// RefreshToken rotates refresh tokens for legacy auth tests.
func (h *AuthHandlers) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}
	if req.RefreshToken == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_TOKEN", "Refresh token is required"))
		return
	}
	if h.authRepo == nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Auth repository not configured"))
		return
	}

	existing, err := h.authRepo.GetRefreshToken(r.Context(), req.RefreshToken)
	if err != nil {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrInvalidToken)
		return
	}

	_ = h.authRepo.RevokeRefreshToken(r.Context(), req.RefreshToken)

	newRefresh := uuid.NewString()
	refreshExpires := time.Now().Add(7 * 24 * time.Hour)
	rt := &usecase.RefreshToken{
		ID:        uuid.NewString(),
		UserID:    existing.UserID,
		Token:     newRefresh,
		ExpiresAt: refreshExpires,
		CreatedAt: time.Now(),
	}
	if err := h.authRepo.CreateRefreshToken(r.Context(), rt); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainErrorWithDetails("TOKEN_ISSUE_FAILED", "Failed to issue refresh token", err.Error()))
		return
	}
	_ = h.authRepo.DeleteSession(r.Context(), req.RefreshToken)
	if err := h.authRepo.CreateSession(r.Context(), newRefresh, existing.UserID, refreshExpires); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("SESSION_CREATE_FAILED", "Failed to create session"))
		return
	}

	role := ""
	if h.userRepo != nil {
		if user, userErr := h.userRepo.GetByID(r.Context(), existing.UserID); userErr == nil {
			role = string(user.Role)
		}
	}

	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"access_token":  h.generateJWTWithRole(existing.UserID, role, 15*time.Minute),
		"refresh_token": newRefresh,
		"expires_in":    int64(15 * 60),
	})
}

// Logout revokes all refresh tokens and sessions for the current user.
func (h *AuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.UserIDKey).(string)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
		return
	}

	if h.authRepo != nil {
		_ = h.authRepo.RevokeAllUserTokens(r.Context(), userID)
		_ = h.authRepo.DeleteAllUserSessions(r.Context(), userID)
	}

	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"message": "Logged out successfully",
		"user_id": userID,
	})
}
