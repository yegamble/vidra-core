package auth

import (
	"context"
	"encoding/json"
	"net/http"

	"golang.org/x/crypto/bcrypt"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
)

// AccountDeletionRepository defines the user operations needed for account self-deletion.
type AccountDeletionRepository interface {
	GetByID(ctx context.Context, id string) (*domain.User, error)
	GetPasswordHash(ctx context.Context, userID string) (string, error)
	// Anonymize soft-deletes the user by setting is_active=false and anonymizing PII.
	// Videos owned by the user are retained with "Deleted User" attribution.
	// Storage assets (IPFS pins, S3 objects) are NOT cleaned up — videos remain accessible.
	Anonymize(ctx context.Context, userID string) error
}

// DeleteAccountHandler handles DELETE /api/v1/users/me.
// The user must confirm their password. On success, the account is soft-deleted and
// all PII is anonymized. The user's videos remain with "Deleted User" attribution.
func DeleteAccountHandler(repo AccountDeletionRepository) http.HandlerFunc {
	type deleteRequest struct {
		Password string `json:"password"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
			return
		}

		var req deleteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
			return
		}

		if req.Password == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_PASSWORD", "Password confirmation is required"))
			return
		}

		// Verify the user exists
		if _, err := repo.GetByID(r.Context(), userID); err != nil {
			if err == domain.ErrUserNotFound {
				shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("USER_NOT_FOUND", "User not found"))
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to load user"))
			return
		}

		// Verify password
		hash, err := repo.GetPasswordHash(r.Context(), userID)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to verify password"))
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("INVALID_PASSWORD", "Incorrect password"))
			return
		}

		// Anonymize the account
		if err := repo.Anonymize(r.Context(), userID); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to delete account"))
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
