package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/repository"
)

// PasswordResetUserRepository defines the user operations needed for password reset.
type PasswordResetUserRepository interface {
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	GetByID(ctx context.Context, id string) (*domain.User, error)
	UpdatePassword(ctx context.Context, userID, passwordHash string) error
}

// PasswordResetEmailService defines the email operation needed for password reset.
type PasswordResetEmailService interface {
	SendPasswordResetEmail(ctx context.Context, toEmail, username, token string) error
}

// PasswordResetHandlers handles password reset endpoints.
type PasswordResetHandlers struct {
	resetRepo repository.PasswordResetRepository
	userRepo  PasswordResetUserRepository
	emailSvc  PasswordResetEmailService
}

// NewPasswordResetHandlers creates a new PasswordResetHandlers.
func NewPasswordResetHandlers(
	resetRepo repository.PasswordResetRepository,
	userRepo PasswordResetUserRepository,
	emailSvc PasswordResetEmailService,
) *PasswordResetHandlers {
	return &PasswordResetHandlers{
		resetRepo: resetRepo,
		userRepo:  userRepo,
		emailSvc:  emailSvc,
	}
}

type askResetPasswordRequest struct {
	Email string `json:"email"`
}

type resetPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// AskResetPassword handles POST /api/v1/users/ask-reset-password.
// Accepts an email, sends a reset token if the user exists. Always returns 204 to avoid
// leaking whether an email address is registered.
func (h *PasswordResetHandlers) AskResetPassword(w http.ResponseWriter, r *http.Request) {
	var req askResetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}
	if req.Email == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_EMAIL", "Email is required"))
		return
	}

	ctx := r.Context()
	user, err := h.userRepo.GetByEmail(ctx, req.Email)
	if err != nil {
		// Return 204 regardless — don't leak whether the email exists
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Generate a cryptographically random token
	rawToken, tokenHash, err := generateResetToken()
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to generate reset token"))
		return
	}

	token := &domain.PasswordResetToken{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(time.Hour),
		CreatedAt: time.Now(),
	}

	if err := h.resetRepo.CreateToken(ctx, token); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to create reset token"))
		return
	}

	// Send email — ignore error to not block response (email failures are logged externally)
	_ = h.emailSvc.SendPasswordResetEmail(ctx, user.Email, user.Username, rawToken)

	w.WriteHeader(http.StatusNoContent)
}

// ResetPassword handles POST /api/v1/users/{id}/reset-password.
// Accepts a reset token and new password. Returns 204 on success, 403 on invalid/expired token.
func (h *PasswordResetHandlers) ResetPassword(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")

	var req resetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}
	if req.Token == "" || req.Password == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FIELDS", "token and password are required"))
		return
	}

	ctx := r.Context()
	tokenHash := hashResetToken(req.Token)

	resetToken, err := h.resetRepo.GetByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidToken) {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("INVALID_TOKEN", "Invalid or expired reset token"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to validate reset token"))
		return
	}

	if resetToken.IsExpired() {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("TOKEN_EXPIRED", "Reset token has expired"))
		return
	}

	if resetToken.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("INVALID_TOKEN", "Invalid reset token for this user"))
		return
	}

	passwordHash, err := hashPassword(req.Password)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to hash password"))
		return
	}

	if err := h.userRepo.UpdatePassword(ctx, userID, passwordHash); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update password"))
		return
	}

	if err := h.resetRepo.MarkUsed(ctx, resetToken.ID); err != nil {
		// Password was updated; log but don't fail the request
		_ = err
	}

	w.WriteHeader(http.StatusNoContent)
}

// generateResetToken creates a random token and returns (rawToken, sha256Hash, error).
func generateResetToken() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generating reset token: %w", err)
	}
	raw := hex.EncodeToString(b)
	return raw, hashResetToken(raw), nil
}

// hashResetToken returns the SHA-256 hex digest of the given reset token.
func hashResetToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// hashPassword hashes a plaintext password using bcrypt.
func hashPassword(password string) (string, error) {
	hash, err := generatePasswordHash(password)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}
	return hash, nil
}
