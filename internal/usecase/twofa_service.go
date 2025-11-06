package usecase

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"athena/internal/domain"
)

// TwoFAService handles two-factor authentication operations
type TwoFAService struct {
	userRepo       UserRepository
	backupCodeRepo TwoFABackupCodeRepository
	issuer         string // Application name for TOTP (e.g., "Athena")
}

// NewTwoFAService creates a new 2FA service
func NewTwoFAService(userRepo UserRepository, backupCodeRepo TwoFABackupCodeRepository, issuer string) *TwoFAService {
	if issuer == "" {
		issuer = "Athena"
	}
	return &TwoFAService{
		userRepo:       userRepo,
		backupCodeRepo: backupCodeRepo,
		issuer:         issuer,
	}
}

// GenerateSecret generates a new TOTP secret for a user
func (s *TwoFAService) GenerateSecret(ctx context.Context, userID string) (*domain.TwoFASetupResponse, error) {
	// Get user to check if 2FA is already enabled
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if user.TwoFAEnabled {
		return nil, domain.ErrTwoFAAlreadyEnabled
	}

	// Generate TOTP key
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      s.issuer,
		AccountName: user.Email,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate TOTP key: %w", err)
	}

	// Store the secret (but don't enable 2FA yet - that happens after verification)
	user.TwoFASecret = key.Secret()
	user.TwoFAEnabled = false // Not enabled until verified
	user.UpdatedAt = time.Now()

	if err := s.userRepo.Update(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to update user with 2FA secret: %w", err)
	}

	// Generate backup codes
	backupCodes, err := s.generateBackupCodes(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate backup codes: %w", err)
	}

	// Return setup response
	return &domain.TwoFASetupResponse{
		Secret:      key.Secret(),
		QRCodeURI:   key.URL(),
		BackupCodes: backupCodes,
	}, nil
}

// VerifySetup verifies the TOTP code and enables 2FA for the user
func (s *TwoFAService) VerifySetup(ctx context.Context, userID, code string) error {
	// Get user
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	if user.TwoFAEnabled {
		return domain.ErrTwoFAAlreadyEnabled
	}

	if user.TwoFASecret == "" {
		return domain.ErrTwoFASetupIncomplete
	}

	// Verify the TOTP code
	valid := totp.Validate(code, user.TwoFASecret)
	if !valid {
		return domain.ErrTwoFAInvalidCode
	}

	// Enable 2FA
	user.TwoFAEnabled = true
	user.TwoFAConfirmedAt.Time = time.Now()
	user.TwoFAConfirmedAt.Valid = true
	user.UpdatedAt = time.Now()

	if err := s.userRepo.Update(ctx, user); err != nil {
		return fmt.Errorf("failed to enable 2FA: %w", err)
	}

	return nil
}

// VerifyCode verifies a TOTP code or backup code for a user
func (s *TwoFAService) VerifyCode(ctx context.Context, userID, code string) error {
	// Get user
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	if !user.TwoFAEnabled {
		return domain.ErrTwoFANotEnabled
	}

	// Clean the code (remove spaces, dashes, etc.)
	code = cleanCode(code)

	// Try TOTP first (6 digits)
	if len(code) == 6 {
		valid := totp.Validate(code, user.TwoFASecret)
		if valid {
			return nil
		}
	}

	// Try backup code (typically 8-10 characters)
	if len(code) >= 8 {
		if err := s.verifyBackupCode(ctx, userID, code); err == nil {
			return nil
		}
	}

	return domain.ErrTwoFAInvalidCode
}

// Disable disables 2FA for a user
func (s *TwoFAService) Disable(ctx context.Context, userID, password, code string) error {
	// Get user
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	if !user.TwoFAEnabled {
		return domain.ErrTwoFANotEnabled
	}

	// Verify password
	hash, err := s.userRepo.GetPasswordHash(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get password hash: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return domain.ErrInvalidCredentials
	}

	// Verify 2FA code
	if err := s.VerifyCode(ctx, userID, code); err != nil {
		return err
	}

	// Disable 2FA
	user.TwoFAEnabled = false
	user.TwoFASecret = ""
	user.TwoFAConfirmedAt.Valid = false
	user.UpdatedAt = time.Now()

	if err := s.userRepo.Update(ctx, user); err != nil {
		return fmt.Errorf("failed to disable 2FA: %w", err)
	}

	// Delete all backup codes
	if err := s.backupCodeRepo.DeleteAllForUser(ctx, userID); err != nil {
		return fmt.Errorf("failed to delete backup codes: %w", err)
	}

	return nil
}

// RegenerateBackupCodes regenerates backup codes for a user
func (s *TwoFAService) RegenerateBackupCodes(ctx context.Context, userID, code string) ([]string, error) {
	// Get user
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if !user.TwoFAEnabled {
		return nil, domain.ErrTwoFANotEnabled
	}

	// Verify 2FA code
	if err := s.VerifyCode(ctx, userID, code); err != nil {
		return nil, err
	}

	// Delete old backup codes
	if err := s.backupCodeRepo.DeleteAllForUser(ctx, userID); err != nil {
		return nil, fmt.Errorf("failed to delete old backup codes: %w", err)
	}

	// Generate new backup codes
	backupCodes, err := s.generateBackupCodes(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate new backup codes: %w", err)
	}

	return backupCodes, nil
}

// generateBackupCodes generates a set of backup codes for a user
func (s *TwoFAService) generateBackupCodes(ctx context.Context, userID string) ([]string, error) {
	const numCodes = 10
	const codeLength = 8

	codes := make([]string, numCodes)

	for i := 0; i < numCodes; i++ {
		// Generate random code
		code, err := generateRandomCode(codeLength)
		if err != nil {
			return nil, fmt.Errorf("failed to generate random code: %w", err)
		}

		codes[i] = code

		// Hash and store the code
		hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("failed to hash backup code: %w", err)
		}

		backupCode := &domain.TwoFABackupCode{
			UserID:    userID,
			CodeHash:  string(hash),
			CreatedAt: time.Now(),
		}

		if err := s.backupCodeRepo.Create(ctx, backupCode); err != nil {
			return nil, fmt.Errorf("failed to store backup code: %w", err)
		}
	}

	return codes, nil
}

// verifyBackupCode verifies and marks a backup code as used
func (s *TwoFAService) verifyBackupCode(ctx context.Context, userID, code string) error {
	// Get all unused backup codes for the user
	backupCodes, err := s.backupCodeRepo.GetUnusedForUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get backup codes: %w", err)
	}

	// Try to match the code
	for _, bc := range backupCodes {
		if err := bcrypt.CompareHashAndPassword([]byte(bc.CodeHash), []byte(code)); err == nil {
			// Mark as used
			if err := s.backupCodeRepo.MarkAsUsed(ctx, bc.ID); err != nil {
				return fmt.Errorf("failed to mark backup code as used: %w", err)
			}
			return nil
		}
	}

	return domain.ErrTwoFAInvalidCode
}

// generateRandomCode generates a random alphanumeric code
func generateRandomCode(length int) (string, error) {
	// Use base32 encoding for human-readable codes (no ambiguous characters)
	// We need more random bytes than the desired length due to base32 encoding
	randomBytes := make([]byte, length)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}

	// Encode to base32 and take the first 'length' characters
	encoded := base32.StdEncoding.EncodeToString(randomBytes)
	encoded = strings.ToUpper(encoded)

	// Remove padding and take only the desired length
	encoded = strings.ReplaceAll(encoded, "=", "")
	if len(encoded) > length {
		encoded = encoded[:length]
	}

	// Format with dash for readability (e.g., ABCD-EFGH)
	if length == 8 {
		encoded = encoded[:4] + "-" + encoded[4:]
	}

	return encoded, nil
}

// cleanCode removes spaces, dashes, and converts to uppercase
func cleanCode(code string) string {
	code = strings.ReplaceAll(code, " ", "")
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ToUpper(code)
	return code
}
