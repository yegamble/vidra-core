// Package generated provides types and interfaces generated from OpenAPI specification.
// Code generated from OpenAPI spec. DO NOT EDIT.
package generated

import (
	"time"
)

// User represents a user in the system
type User struct {
	ID              string    `json:"id"`
	Username        string    `json:"username"`
	Email           string    `json:"email"`
	DisplayName     *string   `json:"display_name,omitempty"`
	Avatar          *Avatar   `json:"avatar,omitempty"`
	Bio             *string   `json:"bio,omitempty"`
	Role            UserRole  `json:"role"`
	IsActive        bool      `json:"is_active"`
	SubscriberCount int64     `json:"subscriber_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Avatar represents a user's avatar metadata
type Avatar struct {
	ID          string  `json:"id"`
	IpfsCid     *string `json:"ipfs_cid"`
	WebpIpfsCid *string `json:"webp_ipfs_cid"`
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
	Email    string `json:"email"`
	Password string `json:"password"`
}

// RegisterRequest represents a registration request
type RegisterRequest struct {
	Username    string  `json:"username"`
	Email       string  `json:"email"`
	Password    string  `json:"password"`
	DisplayName *string `json:"display_name,omitempty"`
}

// RefreshTokenRequest represents a token refresh request
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
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
	Status    ReadinessResponseStatus `json:"status"`
	Checks    ReadinessResponseChecks `json:"checks"`
	Timestamp time.Time               `json:"timestamp"`
}

type ReadinessResponseStatus string

const (
	ReadinessStatusReady    ReadinessResponseStatus = "ready"
	ReadinessStatusNotReady ReadinessResponseStatus = "not_ready"
)

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

// Service health status constants
const (
	ServiceStatusHealthy   = "healthy"
	ServiceStatusUnhealthy = "unhealthy"
)

// Minimal video and wrappers to satisfy API responses
type Video struct {
	ID          string    `json:"id"`
	ThumbnailId string    `json:"thumbnail_id"`
	Title       string    `json:"title"`
	Description *string   `json:"description,omitempty"`
	Duration    *int      `json:"duration,omitempty"`
	Views       *int64    `json:"views,omitempty"`
	Privacy     string    `json:"privacy"`
	Status      string    `json:"status"`
	UploadDate  time.Time `json:"upload_date"`
	UserId      string    `json:"user_id"`
	Tags        *[]string `json:"tags,omitempty"`
	Category    *string   `json:"category,omitempty"`
	Language    *string   `json:"language,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ListMeta struct {
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

type WrappedVideosResponse struct {
	Data    []Video  `json:"data"`
	Meta    ListMeta `json:"meta"`
	Success bool     `json:"success"`
}

type WrappedVideoResponse struct {
	Data    Video `json:"data"`
	Success bool  `json:"success"`
}

type WrappedUserResponse struct {
	Data    User `json:"data"`
	Success bool `json:"success"`
}

type WrappedUsersResponse struct {
	Data    []User   `json:"data"`
	Meta    ListMeta `json:"meta"`
	Success bool     `json:"success"`
}
