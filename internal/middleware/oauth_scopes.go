package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// OAuth2 Scopes
const (
	ScopeBasic     = "basic"     // Basic read access
	ScopeProfile   = "profile"   // Read user profile
	ScopeEmail     = "email"     // Access email
	ScopeUpload    = "upload"    // Upload videos
	ScopeWrite     = "write"     // Write access (create, update, delete)
	ScopeModerate  = "moderate"  // Moderation capabilities
	ScopeAdmin     = "admin"     // Full admin access
	ScopeMessages  = "messages"  // Access messages
	ScopeSubscribe = "subscribe" // Manage subscriptions
	ScopeComment   = "comment"   // Comment on videos
	ScopeRate      = "rate"      // Rate videos
	ScopePlaylist  = "playlist"  // Manage playlists
)

type scopesKey struct{}
type tokenKeyType struct{}

var tokenKey = tokenKeyType{}

// RequireScopes creates a middleware that ensures the request has all required OAuth scopes
func RequireScopes(requiredScopes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get JWT token from context
			token, ok := r.Context().Value(tokenKey).(*jwt.Token)
			if !ok || token == nil {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Extract scopes from token claims
			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				http.Error(w, "Invalid token claims", http.StatusUnauthorized)
				return
			}

			// Get scope claim (could be string or []string)
			var tokenScopes []string
			if scopesClaim, exists := claims["scope"]; exists {
				switch v := scopesClaim.(type) {
				case string:
					tokenScopes = strings.Split(v, " ")
				case []interface{}:
					for _, s := range v {
						if str, ok := s.(string); ok {
							tokenScopes = append(tokenScopes, str)
						}
					}
				}
			}

			// Check if all required scopes are present
			for _, required := range requiredScopes {
				found := false
				for _, scope := range tokenScopes {
					if scope == required || scope == ScopeAdmin {
						// Admin scope grants all permissions
						found = true
						break
					}
				}
				if !found {
					w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="`+strings.Join(requiredScopes, " ")+`"`)
					http.Error(w, "Insufficient scope", http.StatusForbidden)
					return
				}
			}

			// Store scopes in context for downstream handlers
			ctx := context.WithValue(r.Context(), scopesKey{}, tokenScopes)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetScopes retrieves scopes from request context
func GetScopes(ctx context.Context) []string {
	if scopes, ok := ctx.Value(scopesKey{}).([]string); ok {
		return scopes
	}
	return []string{}
}

// HasScope checks if the context has a specific scope
func HasScope(ctx context.Context, scope string) bool {
	scopes := GetScopes(ctx)
	for _, s := range scopes {
		if s == scope || s == ScopeAdmin {
			return true
		}
	}
	return false
}

// OptionalScopes is like RequireScopes but doesn't fail if no auth is present
func OptionalScopes(scopes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If there's no token, just proceed without scope check
			token, ok := r.Context().Value(tokenKey).(*jwt.Token)
			if !ok || token == nil {
				next.ServeHTTP(w, r)
				return
			}

			// If there is a token, apply scope requirements
			RequireScopes(scopes...)(next).ServeHTTP(w, r)
		})
	}
}
