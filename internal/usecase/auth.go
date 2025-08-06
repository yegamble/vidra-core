package usecase

import (
    "context"
    "errors"
    "fmt"
    "time"

    "gotube/internal/auth"
    "gotube/internal/model"
    "gotube/internal/repository"
    "gotube/internal/service"

    "github.com/go-redis/redis/v8"
)

// AuthUsecase encapsulates authentication workflows: user registration,
// email verification, login, and token revocation. It depends on
// repositories for persistence, the mailer for sending verification
// emails, the IOTA service for wallet generation, and Redis for token
// management.
type AuthUsecase struct {
    Users  repository.UserRepository
    Mailer *service.Mailer
    IOTA   *service.IOTAService
    JWTSecret string
    TokenExpiry time.Duration
    RedisClient *redis.Client
}

// SignUp creates a new user with the provided email and password. It
// hashes the password, persists the user, generates an IOTA wallet, and
// sends a verification email. It returns the created user on success.
func (u *AuthUsecase) SignUp(ctx context.Context, email, password string) (*model.User, error) {
    // Basic validation
    if email == "" || password == "" {
        return nil, errors.New("email and password are required")
    }
    // Check if user already exists
    if _, err := u.Users.GetByEmail(ctx, email); err == nil {
        return nil, errors.New("email already registered")
    }
    // Hash password
    hash, err := auth.HashPassword(password)
    if err != nil {
        return nil, fmt.Errorf("hash password: %w", err)
    }
    // Generate IOTA wallet (stub)
    wallet, err := u.IOTA.GenerateAddress()
    if err != nil {
        return nil, fmt.Errorf("generate IOTA wallet: %w", err)
    }
    now := time.Now()
    user := &model.User{
        Email:       email,
        PasswordHash: hash,
        Verified:    false,
        IotaWallet:  wallet,
        CreatedAt:   now,
        UpdatedAt:   now,
    }
    if err := u.Users.Create(ctx, user); err != nil {
        return nil, err
    }
    // TODO: generate verification token and store / send email
    // In this stub we simply print to stdout via mailer
    if u.Mailer != nil {
        body := fmt.Sprintf("Welcome to GoTube! Please verify your email address for account %s.", email)
        _ = u.Mailer.Send(email, "Verify your GoTube account", body)
    }
    return user, nil
}

// Login verifies user credentials and returns a JWT token if valid.
// The token is stored in Redis with the configured TTL. On error,
// returns nil token and an error.
func (u *AuthUsecase) Login(ctx context.Context, email, password string) (string, error) {
    user, err := u.Users.GetByEmail(ctx, email)
    if err != nil {
        return "", errors.New("invalid credentials")
    }
    if err := auth.VerifyPassword(user.PasswordHash, password); err != nil {
        return "", errors.New("invalid credentials")
    }
    if !user.Verified {
        return "", errors.New("email not verified")
    }
    token, err := auth.GenerateJWT(user.ID, u.JWTSecret, u.TokenExpiry)
    if err != nil {
        return "", fmt.Errorf("generate token: %w", err)
    }
    // Save to Redis
    if err := auth.SaveToken(ctx, u.RedisClient, token, user.ID, u.TokenExpiry); err != nil {
        return "", fmt.Errorf("save token: %w", err)
    }
    return token, nil
}

// VerifyEmail sets the user as verified. In a real implementation, you
// would look up the user based on a verification token stored in a
// separate table or Redis. Here we just set the flag directly.
func (u *AuthUsecase) VerifyEmail(ctx context.Context, userID int64) error {
    return u.Users.SetVerified(ctx, userID, true)
}

// Logout revokes the provided JWT by deleting it from Redis.
func (u *AuthUsecase) Logout(ctx context.Context, token string) error {
    return auth.RevokeToken(ctx, u.RedisClient, token)
}