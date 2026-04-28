package domain

import (
	"time"

	"github.com/google/uuid"
)

// MembershipStatus is the canonical status for an Inner Circle membership.
type MembershipStatus string

const (
	MembershipStatusActive    MembershipStatus = "active"
	MembershipStatusPending   MembershipStatus = "pending"
	MembershipStatusCancelled MembershipStatus = "cancelled"
	MembershipStatusExpired   MembershipStatus = "expired"
)

// IsValid reports whether the status is a known canonical constant.
func (s MembershipStatus) IsValid() bool {
	switch s {
	case MembershipStatusActive, MembershipStatusPending, MembershipStatusCancelled, MembershipStatusExpired:
		return true
	}
	return false
}

// InnerCircleTier captures per-channel pricing + perks for a single fixed tier
// ID (supporter / vip / elite). Loaded by the channel-page Inner Circle tab and
// the studio settings page.
type InnerCircleTier struct {
	ID              uuid.UUID `db:"id"`
	ChannelID       uuid.UUID `db:"channel_id"`
	TierID          string    `db:"tier_id"`
	MonthlyUSDCents int       `db:"monthly_usd_cents"`
	MonthlySats     int64     `db:"monthly_sats"`
	Perks           []string  `db:"perks"`
	Enabled         bool      `db:"enabled"`
	CreatedAt       time.Time `db:"created_at"`
	UpdatedAt       time.Time `db:"updated_at"`
}

// InnerCircleTierWithCount augments a tier with the count of active members.
// Returned by the tier list endpoint for both viewers and creators.
type InnerCircleTierWithCount struct {
	InnerCircleTier
	MemberCount int `db:"member_count"`
}

// InnerCircleMembership records that a user holds a tier on a channel,
// activated via either Polar (card, recurring) or BTCPay (Bitcoin, 30-day).
type InnerCircleMembership struct {
	ID                  uuid.UUID        `db:"id"`
	UserID              uuid.UUID        `db:"user_id"`
	ChannelID           uuid.UUID        `db:"channel_id"`
	TierID              string           `db:"tier_id"`
	Status              MembershipStatus `db:"status"`
	StartedAt           *time.Time       `db:"started_at"`
	ExpiresAt           time.Time        `db:"expires_at"`
	PolarSubscriptionID *string          `db:"polar_subscription_id"`
	BTCPayInvoiceID     *uuid.UUID       `db:"btcpay_invoice_id"`
	CreatedAt           time.Time        `db:"created_at"`
	UpdatedAt           time.Time        `db:"updated_at"`
}

// ChannelPost is a text-only post on a channel's Members tab. tier_id NULL
// means public visibility; non-NULL gates by the tier hierarchy.
type ChannelPost struct {
	ID        uuid.UUID `db:"id"`
	ChannelID uuid.UUID `db:"channel_id"`
	Body      string    `db:"body"`
	TierID    *string   `db:"tier_id"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}
