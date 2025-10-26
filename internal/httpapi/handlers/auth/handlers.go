package auth

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
	jwt "github.com/golang-jwt/jwt/v5"
	redis "github.com/redis/go-redis/v9"

	"athena/internal/config"
	"athena/internal/usecase"
)

// AuthHandlers holds dependencies for authentication handlers
type AuthHandlers struct {
	userRepo            usecase.UserRepository
	authRepo            usecase.AuthRepository
	oauthRepo           usecase.OAuthRepository
	verificationService *usecase.EmailVerificationService
	jwtSecret           string
	redis               *redis.Client
	redisPingTimeout    time.Duration
	cfg                 *config.Config
}

// NewAuthHandlers creates a new auth handlers instance
func NewAuthHandlers(
	userRepo usecase.UserRepository,
	authRepo usecase.AuthRepository,
	oauthRepo usecase.OAuthRepository,
	verificationService *usecase.EmailVerificationService,
	jwtSecret string,
	redisClient *redis.Client,
	redisPingTimeout time.Duration,
	cfg *config.Config,
) *AuthHandlers {
	return &AuthHandlers{
		userRepo:            userRepo,
		authRepo:            authRepo,
		oauthRepo:           oauthRepo,
		verificationService: verificationService,
		jwtSecret:           jwtSecret,
		redis:               redisClient,
		redisPingTimeout:    redisPingTimeout,
		cfg:                 cfg,
	}
}

// generateJWTWithRole creates a signed JWT including the role claim
func (h *AuthHandlers) generateJWTWithRole(userID string, role string, duration time.Duration) string {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub": userID,
		"iat": now.Unix(),
		"exp": now.Add(duration).Unix(),
	}
	if role != "" {
		claims["role"] = role
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	sgn, err := token.SignedString([]byte(h.jwtSecret))
	if err != nil {
		return ""
	}
	return sgn
}

// generateJWTWithRoleAndScope creates a signed JWT including role and scope claims
func (h *AuthHandlers) generateJWTWithRoleAndScope(userID, role, scope string, duration time.Duration) string {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub": userID,
		"iat": now.Unix(),
		"exp": now.Add(duration).Unix(),
	}
	if role != "" {
		claims["role"] = role
	}
	if scope != "" {
		claims["scope"] = scope
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	sgn, err := token.SignedString([]byte(h.jwtSecret))
	if err != nil {
		return ""
	}
	return sgn
}
