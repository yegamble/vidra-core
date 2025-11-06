package domain

import (
	"database/sql"
	"errors"
	"time"
)

// TwoFABackupCode represents a one-time backup code for 2FA recovery
type TwoFABackupCode struct {
	ID        string       `json:"id" db:"id"`
	UserID    string       `json:"user_id" db:"user_id"`
	CodeHash  string       `json:"-" db:"code_hash"` // Never expose in JSON
	UsedAt    sql.NullTime `json:"used_at,omitempty" db:"used_at"`
	CreatedAt time.Time    `json:"created_at" db:"created_at"`
}

// TwoFASetupRequest is the request to initiate 2FA setup
type TwoFASetupRequest struct {
	// No fields needed for initial setup request
}

// TwoFASetupResponse contains the data needed to set up 2FA
type TwoFASetupResponse struct {
	Secret      string   `json:"secret"`       // Base32-encoded secret for manual entry
	QRCodeURI   string   `json:"qr_code_uri"`  // otpauth:// URI for QR code generation
	BackupCodes []string `json:"backup_codes"` // One-time backup codes (plaintext, only shown once)
}

// TwoFAVerifySetupRequest is the request to verify and enable 2FA
type TwoFAVerifySetupRequest struct {
	Code string `json:"code" validate:"required,len=6"` // 6-digit TOTP code
}

// TwoFAVerifySetupResponse is the response after successfully enabling 2FA
type TwoFAVerifySetupResponse struct {
	Enabled bool `json:"enabled"`
}

// TwoFADisableRequest is the request to disable 2FA
type TwoFADisableRequest struct {
	Password string `json:"password" validate:"required"` // Require password for security
	Code     string `json:"code" validate:"required"`     // Require current 2FA code or backup code
}

// TwoFADisableResponse is the response after disabling 2FA
type TwoFADisableResponse struct {
	Disabled bool `json:"disabled"`
}

// TwoFARegenerateBackupCodesRequest is the request to regenerate backup codes
type TwoFARegenerateBackupCodesRequest struct {
	Code string `json:"code" validate:"required"` // Current 2FA code
}

// TwoFARegenerateBackupCodesResponse contains new backup codes
type TwoFARegenerateBackupCodesResponse struct {
	BackupCodes []string `json:"backup_codes"`
}

// TwoFAVerifyLoginRequest is used during login to verify 2FA code
type TwoFAVerifyLoginRequest struct {
	Code string `json:"code" validate:"required"` // 6-digit TOTP code or backup code
}

// Domain errors for 2FA
var (
	ErrTwoFAAlreadyEnabled  = errors.New("two-factor authentication is already enabled")
	ErrTwoFANotEnabled      = errors.New("two-factor authentication is not enabled")
	ErrTwoFAInvalidCode     = errors.New("invalid two-factor authentication code")
	ErrTwoFASetupIncomplete = errors.New("two-factor authentication setup is incomplete")
	ErrTwoFABackupCodeUsed  = errors.New("backup code has already been used")
	ErrTwoFARequired        = errors.New("two-factor authentication code required")
)
