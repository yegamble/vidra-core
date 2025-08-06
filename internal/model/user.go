package model

import "time"

// User represents a registered user in the system. Passwords are stored as
// bcrypt hashes. Verified indicates whether the user has completed email
// verification. IotaWallet stores the user's default IOTA wallet address.
type User struct {
    ID          int64     `db:"id" json:"id"`
    Email       string    `db:"email" json:"email"`
    PasswordHash string    `db:"password_hash" json:"-"`
    Verified    bool      `db:"verified" json:"verified"`
    IotaWallet  string    `db:"iota_wallet" json:"iota_wallet"`
    CreatedAt   time.Time `db:"created_at" json:"created_at"`
    UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`
}