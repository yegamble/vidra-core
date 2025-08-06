package api

import (
    "context"
    "net/http"
    "strings"

    "github.com/go-redis/redis/v8"

    "gotube/internal/auth"
)

// AuthMiddleware verifies JWT tokens on protected routes. It requires
// the JWT secret and Redis client to validate token presence. If
// validation succeeds, the user ID is injected into the context; else
// a 401 Unauthorized response is returned.
func AuthMiddleware(secret string, rdb *redis.Client) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Extract token from Authorization header
            authHeader := r.Header.Get("Authorization")
            if authHeader == "" {
                http.Error(w, "missing Authorization header", http.StatusUnauthorized)
                return
            }
            parts := strings.SplitN(authHeader, " ", 2)
            if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
                http.Error(w, "invalid Authorization header", http.StatusUnauthorized)
                return
            }
            tokenStr := parts[1]
            // Parse and verify signature
            claims, err := auth.ValidateJWT(tokenStr, secret)
            if err != nil {
                http.Error(w, "invalid token", http.StatusUnauthorized)
                return
            }
            // Check if token is present in Redis
            ok, err := auth.IsTokenValid(r.Context(), rdb, tokenStr)
            if err != nil || !ok {
                http.Error(w, "token expired or revoked", http.StatusUnauthorized)
                return
            }
            // Add user ID to context and call next
            ctx := WithUserID(r.Context(), claims.UserID)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}