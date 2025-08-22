package domain

import (
    "database/sql"
    "encoding/json"
    "time"
)

type User struct {
    ID          string    `json:"id" db:"id"`
    Username    string    `json:"username" db:"username"`
    Email       string    `json:"email" db:"email"`
    DisplayName string    `json:"display_name" db:"display_name"`
    // Avatar is stored in a separate table and joined in repository queries
    Avatar *Avatar `json:"-"`
    Bio           string    `json:"bio" db:"bio"`
    BitcoinWallet string    `json:"bitcoin_wallet" db:"bitcoin_wallet"`
    Role          UserRole  `json:"role" db:"role"`
    IsActive      bool      `json:"is_active" db:"is_active"`
    CreatedAt     time.Time `json:"created_at" db:"created_at"`
    UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
}

// Avatar represents a user's avatar metadata
type Avatar struct {
    ID             string         `json:"-" db:"avatar_id"`
    IPFSCID        sql.NullString `json:"-" db:"avatar_ipfs_cid"`
    WebPIPFSCID    sql.NullString `json:"-" db:"avatar_webp_ipfs_cid"`
}

// MarshalJSON implements custom JSON marshaling for User
func (u User) MarshalJSON() ([]byte, error) {
    type Alias User
    // Prepare nested avatar payload if present
    var avatarPayload *struct {
        ID             string  `json:"id"`
        IPFSCID        *string `json:"ipfs_cid"`
        WebPIPFSCID    *string `json:"webp_ipfs_cid"`
    }
    if u.Avatar != nil {
        avatarPayload = &struct {
            ID             string  `json:"id"`
            IPFSCID        *string `json:"ipfs_cid"`
            WebPIPFSCID    *string `json:"webp_ipfs_cid"`
        }{
            ID:          u.Avatar.ID,
            IPFSCID:     nullStringToPtr(u.Avatar.IPFSCID),
            WebPIPFSCID: nullStringToPtr(u.Avatar.WebPIPFSCID),
        }
    }
    return json.Marshal(&struct {
        Avatar *struct {
            ID          string  `json:"id"`
            IPFSCID     *string `json:"ipfs_cid"`
            WebPIPFSCID *string `json:"webp_ipfs_cid"`
        } `json:"avatar,omitempty"`
        *Alias
    }{
        Avatar: avatarPayload,
        Alias:  (*Alias)(&u),
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
