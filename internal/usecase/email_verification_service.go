package usecase

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"

	"athena/internal/domain"
	"athena/internal/email"
)

// EmailVerificationService handles email verification logic
type EmailVerificationService struct {
	userRepo     UserRepository
	verifyRepo   EmailVerificationRepository
	emailService email.EmailService
}

// NewEmailVerificationService creates a new email verification service
func NewEmailVerificationService(
	userRepo UserRepository,
	verifyRepo EmailVerificationRepository,
	emailService email.EmailService,
) *EmailVerificationService {
	return &EmailVerificationService{
		userRepo:     userRepo,
		verifyRepo:   verifyRepo,
		emailService: emailService,
	}
}

// SendVerificationEmail creates a verification token and sends the email
func (s *EmailVerificationService) SendVerificationEmail(ctx context.Context, userID string) error {
	// Get user
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	// Check if already verified
	if user.EmailVerified {
		return domain.ErrEmailAlreadyVerified
	}

	// Revoke any existing tokens for this user
	_ = s.verifyRepo.RevokeAllUserTokens(ctx, userID)

	// Generate token and code
	token := generateToken()
	code := generateCode()

	// Create verification token
	verificationToken := &domain.EmailVerificationToken{
		ID:        uuid.NewString(),
		UserID:    userID,
		Token:     token,
		Code:      code,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}

	// Save token to database
	if err := s.verifyRepo.CreateVerificationToken(ctx, verificationToken); err != nil {
		return fmt.Errorf("failed to create verification token: %w", err)
	}

	// Send email
	if err := s.emailService.SendVerificationEmail(ctx, user.Email, user.Username, token, code); err != nil {
		return fmt.Errorf("failed to send verification email: %w", err)
	}

	return nil
}

// VerifyEmailWithToken verifies email using token
func (s *EmailVerificationService) VerifyEmailWithToken(ctx context.Context, token string) error {
	// Get verification token
	verificationToken, err := s.verifyRepo.GetVerificationToken(ctx, token)
	if err != nil {
		return err
	}

	// Check if token is expired
	if time.Now().After(verificationToken.ExpiresAt) {
		return domain.ErrVerificationTokenExpired
	}

	// Mark email as verified
	if err := s.userRepo.MarkEmailAsVerified(ctx, verificationToken.UserID); err != nil {
		return fmt.Errorf("failed to mark email as verified: %w", err)
	}

	// Mark token as used
	if err := s.verifyRepo.MarkTokenAsUsed(ctx, verificationToken.ID); err != nil {
		return fmt.Errorf("failed to mark token as used: %w", err)
	}

	return nil
}

// VerifyEmailWithCode verifies email using code and user ID
func (s *EmailVerificationService) VerifyEmailWithCode(ctx context.Context, code string, userID string) error {
	// Get verification token by code
	verificationToken, err := s.verifyRepo.GetVerificationTokenByCode(ctx, code, userID)
	if err != nil {
		return err
	}

	// Check if token is expired
	if time.Now().After(verificationToken.ExpiresAt) {
		return domain.ErrVerificationTokenExpired
	}

	// Mark email as verified
	if err := s.userRepo.MarkEmailAsVerified(ctx, verificationToken.UserID); err != nil {
		return fmt.Errorf("failed to mark email as verified: %w", err)
	}

	// Mark token as used
	if err := s.verifyRepo.MarkTokenAsUsed(ctx, verificationToken.ID); err != nil {
		return fmt.Errorf("failed to mark token as used: %w", err)
	}

	return nil
}

// ResendVerificationEmail resends the verification email
func (s *EmailVerificationService) ResendVerificationEmail(ctx context.Context, email string) error {
	// Get user by email
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	// Check if already verified
	if user.EmailVerified {
		return domain.ErrEmailAlreadyVerified
	}

	// Check if there's an existing valid token
	existingToken, err := s.verifyRepo.GetLatestTokenForUser(ctx, user.ID)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to check existing token: %w", err)
	}

	// If there's a recent token (created within last 5 minutes), don't create a new one
	if existingToken != nil && time.Since(existingToken.CreatedAt) < 5*time.Minute {
		// Resend the existing token
		if err := s.emailService.SendResendVerificationEmail(ctx, user.Email, user.Username, existingToken.Token, existingToken.Code); err != nil {
			return fmt.Errorf("failed to resend verification email: %w", err)
		}
		return nil
	}

	// Otherwise, create a new token
	return s.SendVerificationEmail(ctx, user.ID)
}

// CleanupExpiredTokens removes expired verification tokens
func (s *EmailVerificationService) CleanupExpiredTokens(ctx context.Context) error {
	return s.verifyRepo.DeleteExpiredTokens(ctx)
}

// generateToken generates a secure random token
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Fallback to UUID if random fails
		return uuid.NewString()
	}
	return hex.EncodeToString(b)
}

// generateCode generates a 6-digit verification code
func generateCode() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		// Fallback to a deterministic code if random fails
		return "123456"
	}
	// Convert to 6-digit number
	num := int(b[0])<<16 | int(b[1])<<8 | int(b[2])
	code := fmt.Sprintf("%06d", num%1000000)
	return code
}
