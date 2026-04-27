package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"vidra-core/internal/domain"
)

// WSAuth authenticates WebSocket upgrade requests by extracting a JWT in priority order:
//  1. Sec-WebSocket-Protocol: access_token, <token>   (browser-friendly: WebSocket API supports subprotocol header)
//  2. ?token=<token>                                   (fallback for client libraries that don't support subprotocols)
//  3. Authorization: Bearer <token>                    (rare; non-browser clients)
//
// On success, injects UserIDKey (and UserRoleKey when present) into the request context. The
// handler chained behind WSAuth is responsible for the actual websocket Upgrade — middleware
// only authenticates. The upgrader must declare Subprotocols: []string{"access_token"} so the
// 101 response echoes the matching protocol back to the client and completes the handshake.
func WSAuth(jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenString, err := extractWSToken(r)
			if err != nil {
				writeError(w, http.StatusUnauthorized, domain.NewDomainError("MISSING_AUTH", err.Error()))
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

// extractWSToken pulls the JWT out of (in order) the Sec-WebSocket-Protocol header, the ?token
// query parameter, or the Authorization Bearer header. Returns an error only when no source
// produced a token; signature/claim validity is checked separately by validateJWT.
func extractWSToken(r *http.Request) (string, error) {
	if proto := r.Header.Get("Sec-WebSocket-Protocol"); proto != "" {
		if tok, ok := parseAccessTokenSubprotocol(proto); ok {
			return tok, nil
		}
		// Subprotocol header was set but didn't carry a token via the access_token marker.
		// Falling through to other sources would let a misconfigured client succeed in a way
		// that masks the bug; return an explicit error instead.
		return "", errors.New("Sec-WebSocket-Protocol present but missing access_token marker")
	}

	if tok := r.URL.Query().Get("token"); tok != "" {
		return tok, nil
	}

	if auth := r.Header.Get("Authorization"); auth != "" {
		tok := strings.TrimPrefix(auth, "Bearer ")
		if tok != auth && tok != "" {
			return tok, nil
		}
		return "", errors.New("Authorization header set but not a Bearer token")
	}

	return "", errors.New("missing WebSocket auth: expected Sec-WebSocket-Protocol, ?token, or Authorization header")
}

// parseAccessTokenSubprotocol parses the comma-separated Sec-WebSocket-Protocol header expecting
// the form "access_token, <jwt>". Returns the token and true on success.
func parseAccessTokenSubprotocol(header string) (string, bool) {
	parts := strings.Split(header, ",")
	if len(parts) < 2 {
		return "", false
	}
	if strings.TrimSpace(parts[0]) != "access_token" {
		return "", false
	}
	tok := strings.TrimSpace(parts[1])
	if tok == "" {
		return "", false
	}
	return tok, true
}
