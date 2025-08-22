// Package generated provides types and interfaces generated from OpenAPI specification.
// Code generated from OpenAPI spec. DO NOT EDIT.
package generated

import (
	"time"
)

// User represents a user in the system
type User struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	Email       string    `json:"email"`
	DisplayName *string   `json:"display_name,omitempty"`
	Avatar      *Avatar   `json:"avatar,omitempty"`
	Bio         *string   `json:"bio,omitempty"`
	Role        UserRole  `json:"role"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Avatar represents a user's avatar metadata
type Avatar struct {
	ID          string  `json:"id"`
	IPFSCID     *string `json:"ipfs_cid"`
	WebPIPFSCID *string `json:"webp_ipfs_cid"`
}

// UserRole represents the role of a user
type UserRole string

const (
	UserRoleUser      UserRole = "user"
	UserRoleAdmin     UserRole = "admin"
	UserRoleModerator UserRole = "moderator"
)

// LoginRequest represents a login request
type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=8"`
}

// RegisterRequest represents a registration request
type RegisterRequest struct {
	Username    string  `json:"username" validate:"required,min=3,max=50"`
	Email       string  `json:"email" validate:"required,email"`
	Password    string  `json:"password" validate:"required,min=8"`
	DisplayName *string `json:"display_name,omitempty" validate:"max=100"`
}

// RefreshTokenRequest represents a token refresh request
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

// AuthResponse represents an authentication response
type AuthResponse struct {
	User         User   `json:"user"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// TokenResponse represents a token refresh response
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// LogoutResponse represents a logout response
type LogoutResponse struct {
	Message string  `json:"message"`
	UserID  *string `json:"user_id,omitempty"`
}

// HealthResponse represents a health check response
type HealthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

// ReadinessResponse represents a readiness check response
type ReadinessResponse struct {
	Status    string                  `json:"status"`
	Checks    ReadinessResponseChecks `json:"checks"`
	Timestamp time.Time               `json:"timestamp"`
}

// ReadinessResponseChecks represents the checks in a readiness response
type ReadinessResponseChecks struct {
	Database *string `json:"database,omitempty"`
	Redis    *string `json:"redis,omitempty"`
	IPFS     *string `json:"ipfs,omitempty"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error ErrorDetails `json:"error"`
}

// ErrorDetails represents the details of an error
type ErrorDetails struct {
	Code    string                 `json:"code"`
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details"`
}

// Health status constants
const (
	HealthStatusHealthy = "healthy"
)

// Readiness status constants
const (
	ReadinessStatusReady    = "ready"
	ReadinessStatusNotReady = "not_ready"
)

// Service health status constants
const (
	ServiceStatusHealthy   = "healthy"
	ServiceStatusUnhealthy = "unhealthy"
)
