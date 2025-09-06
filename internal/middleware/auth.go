package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"athena/internal/domain"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type contextKey string

const (
	UserIDKey   contextKey = "userID"
	UserRoleKey contextKey = "userRole"
)

func Auth(jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeError(w, http.StatusUnauthorized, domain.NewDomainError("MISSING_AUTH", "Missing authorization header"))
				return
			}

			tokenString := strings.TrimPrefix(authHeader, "Bearer ")
			if tokenString == authHeader {
				writeError(w, http.StatusUnauthorized, domain.NewDomainError("INVALID_AUTH_FORMAT", "Invalid authorization header format"))
				return
			}

			userID, role, err := validateJWT(tokenString, jwtSecret)
			if err != nil {
				writeError(w, http.StatusUnauthorized, domain.NewDomainError("INVALID_TOKEN", "Invalid token"))
				return
			}

			ctx := context.WithValue(r.Context(), UserIDKey, userID)
			if role != "" {
				ctx = context.WithValue(ctx, UserRoleKey, role)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func OptionalAuth(jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				next.ServeHTTP(w, r)
				return
			}

			tokenString := strings.TrimPrefix(authHeader, "Bearer ")
			if tokenString != authHeader {
				if userID, role, err := validateJWT(tokenString, jwtSecret); err == nil {
					ctx := context.WithValue(r.Context(), UserIDKey, userID)
					if role != "" {
						ctx = context.WithValue(ctx, UserRoleKey, role)
					}
					r = r.WithContext(ctx)
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

func validateJWT(tokenString, jwtSecret string) (string, string, error) {
	// Parse and validate token
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrTokenSignatureInvalid
		}
		return []byte(jwtSecret), nil
	}, jwt.WithLeeway(2*time.Second))
	if err != nil || !token.Valid {
		return "", "", err
	}

	// Extract subject and role
	if claims, ok := token.Claims.(jwt.MapClaims); ok {
		sub, subOk := claims["sub"].(string)
		role, _ := claims["role"].(string) // role might not be present
		if subOk {
			return sub, role, nil
		}
	}
	return "", "", jwt.ErrTokenMalformed
}

type Response struct {
	Data    interface{} `json:"data,omitempty"`
	Error   *ErrorInfo  `json:"error,omitempty"`
	Success bool        `json:"success"`
}

type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

func writeError(w http.ResponseWriter, statusCode int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	errorInfo := &ErrorInfo{
		Message: err.Error(),
	}

	if domainErr, ok := err.(domain.DomainError); ok {
		errorInfo.Code = domainErr.Code
		errorInfo.Details = domainErr.Details
	}

	response := Response{
		Error:   errorInfo,
		Success: false,
	}

	_ = json.NewEncoder(w).Encode(response)
}

// GetUserIDFromContext retrieves the user ID from the request context
func GetUserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	userIDStr, ok := ctx.Value(UserIDKey).(string)
	if !ok {
		return uuid.UUID{}, false
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return uuid.UUID{}, false
	}

	return userID, true
}

// RequireAuth is a middleware that requires authentication
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Context().Value(UserIDKey)
		if userID == nil {
			writeError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole returns a middleware that requires a specific user role
// It must be used AFTER the Auth middleware which sets the user context
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get user ID from context (set by Auth middleware)
			userIDStr, ok := r.Context().Value(UserIDKey).(string)
			if !ok || userIDStr == "" {
				writeError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
				return
			}

			// Get user role from JWT claims
			// The role should be included in the JWT token claims
			// For now, we'll need to pass the role through context or re-validate the token
			// to extract the role claim

			// Extract role from request context (should be set by Auth middleware)
			userRole, ok := r.Context().Value(UserRoleKey).(string)
			if !ok || userRole == "" {
				writeError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Access denied"))
				return
			}

			// Check if user has the required role
			if userRole != role {
				writeError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Insufficient permissions"))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
