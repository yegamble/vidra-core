package domain

import (
	"database/sql"
	"encoding/json"
	"time"
)

type User struct {
	ID                string         `json:"id" db:"id"`
	Username          string         `json:"username" db:"username"`
	Email             string         `json:"email" db:"email"`
	DisplayName       string         `json:"display_name" db:"display_name"`
	AvatarIPFSCID     sql.NullString `json:"-" db:"avatar_ipfs_cid"`
	AvatarWebPIPFSCID sql.NullString `json:"-" db:"avatar_webp_ipfs_cid"`
	Bio               string         `json:"bio" db:"bio"`
	BitcoinWallet     string         `json:"bitcoin_wallet" db:"bitcoin_wallet"`
	Role              UserRole       `json:"role" db:"role"`
	IsActive          bool           `json:"is_active" db:"is_active"`
	CreatedAt         time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at" db:"updated_at"`
}

// MarshalJSON implements custom JSON marshaling for User
func (u User) MarshalJSON() ([]byte, error) {
	type Alias User
	return json.Marshal(&struct {
		AvatarIPFSCID     *string `json:"avatar_ipfs_cid"`
		AvatarWebPIPFSCID *string `json:"avatar_webp_ipfs_cid"`
		*Alias
	}{
		AvatarIPFSCID:     nullStringToPtr(u.AvatarIPFSCID),
		AvatarWebPIPFSCID: nullStringToPtr(u.AvatarWebPIPFSCID),
		Alias:             (*Alias)(&u),
	})
}

// nullStringToPtr converts sql.NullString to *string for JSON marshaling
func nullStringToPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

type UserRole string

const (
	RoleUser  UserRole = "user"
	RoleAdmin UserRole = "admin"
	RoleMod   UserRole = "moderator"
)

type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=8"`
}

type RegisterRequest struct {
	Username    string `json:"username" validate:"required,min=3,max=50"`
	Email       string `json:"email" validate:"required,email"`
	Password    string `json:"password" validate:"required,min=8"`
	DisplayName string `json:"display_name" validate:"max=100"`
}

type AuthResponse struct {
	User         User   `json:"user"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}
