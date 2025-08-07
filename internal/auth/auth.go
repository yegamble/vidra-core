package auth

import (
	"context"
	"errors"
	"time"

	"github.com/go-redis/redis/v8"
	jwt "github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// HashPassword takes a plaintext password and returns its bcrypt hash
func HashPassword(password string) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hashed), nil
}

// VerifyPassword compares a plaintext password with its bcrypt hash
func VerifyPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// Claims defines the payload stored in JWT tokens
type Claims struct {
	UserID int64 `json:"user_id"`
	jwt.RegisteredClaims
}

// GenerateJWT creates a signed JWT for a given user
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

// ValidateJWT verifies the token signature and returns the embedded claims
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

// SaveToken stores the JWT in Redis with a TTL
func SaveToken(ctx context.Context, rdb *redis.Client, token string, userID int64, expiration time.Duration) error {
	return rdb.Set(ctx, token, userID, expiration).Err()
}

// IsTokenValid checks Redis to ensure the token has not been revoked
func IsTokenValid(ctx context.Context, rdb *redis.Client, token string) (bool, error) {
	exists, err := rdb.Exists(ctx, token).Result()
	if err != nil {
		return false, err
	}
	return exists == 1, nil
}

// RevokeToken deletes a token from Redis
func RevokeToken(ctx context.Context, rdb *redis.Client, token string) error {
	return rdb.Del(ctx, token).Err()
}
