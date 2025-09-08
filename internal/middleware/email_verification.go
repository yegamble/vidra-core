package middleware

import (
	"context"
	"net/http"

	"athena/internal/domain"
	"athena/internal/usecase"
)

type EmailVerifiedKey string

const (
	EmailVerifiedContextKey EmailVerifiedKey = "emailVerified"
)

// RequireEmailVerification creates a middleware that checks if the user has a verified email
func RequireEmailVerification(userRepo usecase.UserRepository) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get user ID from context (set by Auth middleware)
			userID, ok := r.Context().Value(UserIDKey).(string)
			if !ok || userID == "" {
				writeError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
				return
			}

			// Get user from repository
			user, err := userRepo.GetByID(r.Context(), userID)
			if err != nil {
				if err == domain.ErrUserNotFound {
					writeError(w, http.StatusUnauthorized, domain.NewDomainError("USER_NOT_FOUND", "User not found"))
					return
				}
				writeError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to verify user status"))
				return
			}

			// Check if email is verified
			if !user.EmailVerified {
				writeError(w, http.StatusForbidden, domain.NewDomainError("EMAIL_NOT_VERIFIED", "Please verify your email address to access this resource"))
				return
			}

			// Add email verification status to context
			ctx := context.WithValue(r.Context(), EmailVerifiedContextKey, true)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// OptionalEmailVerification adds email verification status to context without blocking
func OptionalEmailVerification(userRepo usecase.UserRepository) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get user ID from context if present
			userID, ok := r.Context().Value(UserIDKey).(string)
			if ok && userID != "" {
				// Get user from repository
				user, err := userRepo.GetByID(r.Context(), userID)
				if err == nil {
					// Add email verification status to context
					ctx := context.WithValue(r.Context(), EmailVerifiedContextKey, user.EmailVerified)
					r = r.WithContext(ctx)
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// GetEmailVerifiedFromContext retrieves the email verification status from context
func GetEmailVerifiedFromContext(ctx context.Context) (bool, bool) {
	verified, ok := ctx.Value(EmailVerifiedContextKey).(bool)
	return verified, ok
}
