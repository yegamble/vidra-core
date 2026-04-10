package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
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
			if tokenString == authHeader {
				next.ServeHTTP(w, r)
				return
			}

			userID, role, err := validateJWT(tokenString, jwtSecret)
			if err != nil {
				next.ServeHTTP(w, r)
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

// IDMappingLookup resolves a PeerTube integer ID to a Vidra Core UUID string.
// Injected via closure to avoid adding repository dependency to middleware package.
type IDMappingLookup func(ctx context.Context, entityType string, peertubeID int) (string, error)

// DualAuth accepts both Vidra Core and PeerTube JWTs during progressive cutover.
// PeerTube tokens (with integer sub claims) are mapped to Vidra Core UUIDs via idLookup.
// When ptSecret is empty, only Vidra Core tokens are accepted (no behavior change).
func DualAuth(vidraSecret, ptSecret string, idLookup IDMappingLookup) func(http.Handler) http.Handler {
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

			// Try Vidra Core secret first
			userID, role, err := validateJWT(tokenString, vidraSecret)
			if err == nil {
				ctx := context.WithValue(r.Context(), UserIDKey, userID)
				if role != "" {
					ctx = context.WithValue(ctx, UserRoleKey, role)
				}
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Try PeerTube secret if configured
			if ptSecret != "" && idLookup != nil {
				userID, role, err = validateJWTDual(tokenString, ptSecret, idLookup, r.Context())
				if err == nil {
					ctx := context.WithValue(r.Context(), UserIDKey, userID)
					if role != "" {
						ctx = context.WithValue(ctx, UserRoleKey, role)
					}
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			writeError(w, http.StatusUnauthorized, domain.NewDomainError("INVALID_TOKEN", "Invalid token"))
		})
	}
}

// DualAuthWithUserLookup is like DualAuth but validates the user exists and is active in DB.
func DualAuthWithUserLookup(vidraSecret, ptSecret string, idLookup IDMappingLookup, lookup UserLookupFunc) func(http.Handler) http.Handler {
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

			// Try Vidra Core secret first
			userID, role, err := validateJWT(tokenString, vidraSecret)
			if err == nil {
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
				return
			}

			// Try PeerTube secret if configured
			if ptSecret != "" && idLookup != nil {
				userID, role, err = validateJWTDual(tokenString, ptSecret, idLookup, r.Context())
				if err == nil {
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
					return
				}
			}

			writeError(w, http.StatusUnauthorized, domain.NewDomainError("INVALID_TOKEN", "Invalid token"))
		})
	}
}

// DualOptionalAuth is like DualAuth but does not reject requests without auth headers.
func DualOptionalAuth(vidraSecret, ptSecret string, idLookup IDMappingLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				next.ServeHTTP(w, r)
				return
			}

			tokenString := strings.TrimPrefix(authHeader, "Bearer ")
			if tokenString == authHeader {
				next.ServeHTTP(w, r)
				return
			}

			// Try Vidra Core secret first
			userID, role, err := validateJWT(tokenString, vidraSecret)
			if err == nil {
				ctx := context.WithValue(r.Context(), UserIDKey, userID)
				if role != "" {
					ctx = context.WithValue(ctx, UserRoleKey, role)
				}
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Try PeerTube secret if configured
			if ptSecret != "" && idLookup != nil {
				userID, role, err = validateJWTDual(tokenString, ptSecret, idLookup, r.Context())
				if err == nil {
					ctx := context.WithValue(r.Context(), UserIDKey, userID)
					if role != "" {
						ctx = context.WithValue(ctx, UserRoleKey, role)
					}
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			// Optional: invalid token doesn't block request
			next.ServeHTTP(w, r)
		})
	}
}

// validateJWTDual validates a PeerTube JWT and maps the integer user ID to a Vidra Core UUID.
func validateJWTDual(tokenString, ptSecret string, idLookup IDMappingLookup, ctx context.Context) (string, string, error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrTokenSignatureInvalid
		}
		return []byte(ptSecret), nil
	}, jwt.WithLeeway(2*time.Second))
	if err != nil || !token.Valid {
		return "", "", err
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", "", jwt.ErrTokenMalformed
	}

	role, _ := claims["role"].(string)

	// PeerTube sub can be string-encoded integer or float64 (Go JSON unmarshals integers as float64).
	// Non-integer sub values are rejected — only Vidra Core JWTs have UUID sub, and those
	// are validated by the first try in DualAuth with the Vidra secret.
	var peertubeID int
	switch sub := claims["sub"].(type) {
	case string:
		n, parseErr := strconv.Atoi(sub)
		if parseErr != nil {
			return "", "", fmt.Errorf("peertube JWT has non-integer sub: %s", sub)
		}
		peertubeID = n
	case float64:
		peertubeID = int(sub)
	default:
		return "", "", jwt.ErrTokenMalformed
	}

	vidraID, lookupErr := idLookup(ctx, "user", peertubeID)
	if lookupErr != nil {
		return "", "", fmt.Errorf("peertube user %d not mapped: %w", peertubeID, lookupErr)
	}

	return vidraID, role, nil
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

			if len(roles) > 0 && !slices.Contains(roles, userRole) {
				writeError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Insufficient permissions"))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
