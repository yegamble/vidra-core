package auth

import (
    "context"
    "errors"
    "time"

    "github.com/go-redis/redis/v8"
    jwt "github.com/golang-jwt/jwt/v5"
    "golang.org/x/crypto/bcrypt"
)

// HashPassword takes a plaintext password and returns its bcrypt hash.
// Bcrypt automatically handles salt generation. Use bcrypt.DefaultCost for a
// reasonable balance between security and performance.
func HashPassword(password string) (string, error) {
    hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
    if err != nil {
        return "", err
    }
    return string(hashed), nil
}

// VerifyPassword compares a plaintext password with its bcrypt hash. Returns
// nil if they match, or an error otherwise.
func VerifyPassword(hash, password string) error {
    return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// Claims defines the payload stored in JWT tokens. It includes the
// user ID and standard claims like expiration. Additional claims can
// be added as needed.
type Claims struct {
    UserID int64 `json:"user_id"`
    jwt.RegisteredClaims
}

// GenerateJWT creates a signed JWT for a given user. It uses the provided
// secret to sign and sets an expiration duration. Caller should store the
// returned token in Redis to enable revocation before expiry.
func GenerateJWT(userID int64, secret string, duration time.Duration) (string, error) {
    now := time.Now()
    claims := Claims{
        UserID: userID,
        RegisteredClaims: jwt.RegisteredClaims{
            ExpiresAt: jwt.NewNumericDate(now.Add(duration)),
            IssuedAt:  jwt.NewNumericDate(now),
        },
    }
    token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
    return token.SignedString([]byte(secret))
}

// ValidateJWT verifies the token signature and returns the embedded claims.
// It does not consult Redis; callers should use IsTokenValid to check revocation.
func ValidateJWT(tokenString, secret string) (*Claims, error) {
    token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
        if token.Method != jwt.SigningMethodHS256 {
            return nil, errors.New("unexpected signing method")
        }
        return []byte(secret), nil
    })
    if err != nil {
        return nil, err
    }
    claims, ok := token.Claims.(*Claims)
    if !ok || !token.Valid {
        return nil, errors.New("invalid token")
    }
    return claims, nil
}

// SaveToken stores the JWT in Redis with a TTL equal to the remaining time
// until expiration. This allows for token revocation and session management.
// The key is the token itself and the value is the user ID.
func SaveToken(ctx context.Context, rdb *redis.Client, token string, userID int64, expiration time.Duration) error {
    return rdb.Set(ctx, token, userID, expiration).Err()
}

// IsTokenValid checks Redis to ensure the token has not been revoked.
// Returns true if the token exists in Redis, false otherwise.
func IsTokenValid(ctx context.Context, rdb *redis.Client, token string) (bool, error) {
    exists, err := rdb.Exists(ctx, token).Result()
    if err != nil {
        return false, err
    }
    return exists == 1, nil
}

// RevokeToken deletes a token from Redis, effectively logging the user out.
func RevokeToken(ctx context.Context, rdb *redis.Client, token string) error {
    return rdb.Del(ctx, token).Err()
}