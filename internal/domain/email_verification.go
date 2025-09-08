package domain

import (
	"errors"
	"time"
)

// EmailVerificationToken represents an email verification token
type EmailVerificationToken struct {
	ID        string     `json:"id" db:"id"`
	UserID    string     `json:"user_id" db:"user_id"`
	Token     string     `json:"token" db:"token"`
	Code      string     `json:"code" db:"code"`
	ExpiresAt time.Time  `json:"expires_at" db:"expires_at"`
	CreatedAt time.Time  `json:"created_at" db:"created_at"`
	UsedAt    *time.Time `json:"used_at" db:"used_at"`
}

// VerifyEmailRequest represents a request to verify an email
type VerifyEmailRequest struct {
	Token string `json:"token" validate:"required_without=Code"`
	Code  string `json:"code" validate:"required_without=Token,len=6"`
}

// ResendVerificationRequest represents a request to resend verification email
type ResendVerificationRequest struct {
	Email string `json:"email" validate:"required,email"`
}

// Email verification related errors
var (
	ErrEmailAlreadyVerified     = errors.New("email already verified")
	ErrVerificationTokenExpired = errors.New("verification token expired")
	ErrInvalidVerificationToken = errors.New("invalid verification token")
	ErrInvalidVerificationCode  = errors.New("invalid verification code")
	ErrEmailNotVerified         = errors.New("email not verified")
)
