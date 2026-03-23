package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"vidra-core/internal/domain"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type ctxKey string

const (
	UserIDKey   ctxKey = "userID"
	UserRoleKey ctxKey = "userRole"
)

type UserLookupFunc func(ctx context.Context, userID string) (*domain.User, error)

func AuthWithUserLookup(jwtSecret string, lookup UserLookupFunc) func(http.Handler) http.Handler {
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

			if lookup != nil {
				dbUser, lookupErr := lookup(r.Context(), userID)
				if lookupErr != nil {
					writeError(w, http.StatusUnauthorized, domain.NewDomainError("USER_NOT_FOUND", "User no longer exists"))
					return
				}
				if !dbUser.IsActive {
					writeError(w, http.StatusUnauthorized, domain.NewDomainError("USER_INACTIVE", "Account is inactive"))
					return
				}
				role = string(dbUser.Role)
			}

			ctx := context.WithValue(r.Context(), UserIDKey, userID)
			if role != "" {
				ctx = context.WithValue(ctx, UserRoleKey, role)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

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
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrTokenSignatureInvalid
		}
		return []byte(jwtSecret), nil
	}, jwt.WithLeeway(2*time.Second))
	if err != nil || !token.Valid {
		return "", "", err
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok {
		sub, subOk := claims["sub"].(string)
		role, _ := claims["role"].(string)
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

func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userIDStr, ok := r.Context().Value(UserIDKey).(string)
			if !ok || userIDStr == "" {
				writeError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
				return
			}

			userRole, ok := r.Context().Value(UserRoleKey).(string)
			if !ok || userRole == "" {
				writeError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Access denied"))
				return
			}

			if len(roles) > 0 {
				authorized := false
				for _, role := range roles {
					if userRole == role {
						authorized = true
						break
					}
				}
				if !authorized {
					writeError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Insufficient permissions"))
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}
