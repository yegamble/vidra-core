package httpapi

import (
	"encoding/json"
	"net/http"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"
)

// EmailVerificationHandlers contains handlers for email verification
type EmailVerificationHandlers struct {
	verificationService *usecase.EmailVerificationService
}

// NewEmailVerificationHandlers creates new email verification handlers
func NewEmailVerificationHandlers(verificationService *usecase.EmailVerificationService) *EmailVerificationHandlers {
	return &EmailVerificationHandlers{
		verificationService: verificationService,
	}
}

// VerifyEmail handles email verification with token or code
func (h *EmailVerificationHandlers) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req domain.VerifyEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	// If token is provided, verify with token
	if req.Token != "" {
		if err := h.verificationService.VerifyEmailWithToken(r.Context(), req.Token); err != nil {
			switch err {
			case domain.ErrInvalidVerificationToken:
				WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_TOKEN", "Invalid or expired verification token"))
			case domain.ErrVerificationTokenExpired:
				WriteError(w, http.StatusBadRequest, domain.NewDomainError("TOKEN_EXPIRED", "Verification token has expired"))
			default:
				WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to verify email"))
			}
			return
		}

		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"message": "Email verified successfully",
			"success": true,
		})
		return
	}

	// If code is provided, verify with code (requires authentication)
	if req.Code != "" {
		userID, ok := r.Context().Value(middleware.UserIDKey).(string)
		if !ok || userID == "" {
			WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required to verify with code"))
			return
		}

		if err := h.verificationService.VerifyEmailWithCode(r.Context(), req.Code, userID); err != nil {
			switch err {
			case domain.ErrInvalidVerificationCode:
				WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_CODE", "Invalid verification code"))
			case domain.ErrVerificationTokenExpired:
				WriteError(w, http.StatusBadRequest, domain.NewDomainError("CODE_EXPIRED", "Verification code has expired"))
			default:
				WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to verify email"))
			}
			return
		}

		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"message": "Email verified successfully",
			"success": true,
		})
		return
	}

	WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_CREDENTIALS", "Either token or code is required"))
}

// ResendVerification resends the verification email
func (h *EmailVerificationHandlers) ResendVerification(w http.ResponseWriter, r *http.Request) {
	var req domain.ResendVerificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	if req.Email == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_EMAIL", "Email is required"))
		return
	}

	if err := h.verificationService.ResendVerificationEmail(r.Context(), req.Email); err != nil {
		switch err {
		case domain.ErrEmailAlreadyVerified:
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("ALREADY_VERIFIED", "Email is already verified"))
		case domain.ErrUserNotFound:
			// Don't reveal if email doesn't exist for security
			WriteJSON(w, http.StatusOK, map[string]interface{}{
				"message": "If the email exists, a verification email has been sent",
				"success": true,
			})
		default:
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to send verification email"))
		}
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Verification email sent successfully",
		"success": true,
	})
}

// GetVerificationStatus returns the current user's email verification status
func (h *EmailVerificationHandlers) GetVerificationStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	// This would normally get the user from the repository
	// For now, we'll return a placeholder
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"email_verified": false,
		"message":        "Email verification status retrieved",
	})
}
