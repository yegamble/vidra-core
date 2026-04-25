package domain

import (
	"database/sql"
	"time"
)

// PayoutStatus mirrors the Postgres payout_status enum (migration 095).
// IMPORTANT: 'executing' was deliberately omitted per spec-review F04.
type PayoutStatus string

const (
	PayoutStatusPending   PayoutStatus = "pending"
	PayoutStatusApproved  PayoutStatus = "approved"
	PayoutStatusCompleted PayoutStatus = "completed"
	PayoutStatusRejected  PayoutStatus = "rejected"
	PayoutStatusCancelled PayoutStatus = "cancelled"
)

// PayoutDestinationType mirrors payout_destination_type enum.
type PayoutDestinationType string

const (
	PayoutDestOnChain    PayoutDestinationType = "on_chain"
	PayoutDestLightning  PayoutDestinationType = "lightning_bolt11"
)

// Payout is one row in btcpay_payouts.
type Payout struct {
	ID                 string                `db:"id" json:"id"`
	RequesterUserID    string                `db:"requester_user_id" json:"requester_user_id"`
	AmountSats         int64                 `db:"amount_sats" json:"amount_sats"`
	Destination        string                `db:"destination" json:"destination"`
	DestinationType    PayoutDestinationType `db:"destination_type" json:"destination_type"`
	Status             PayoutStatus          `db:"status" json:"status"`
	AutoTrigger        bool                  `db:"auto_trigger" json:"auto_trigger"`
	RequestedAt        time.Time             `db:"requested_at" json:"requested_at"`
	ApprovedAt         sql.NullTime          `db:"approved_at" json:"approved_at,omitempty"`
	ApprovedByAdminID  sql.NullString        `db:"approved_by_admin_id" json:"approved_by_admin_id,omitempty"`
	ExecutedAt         sql.NullTime          `db:"executed_at" json:"executed_at,omitempty"`
	Txid               sql.NullString        `db:"txid" json:"txid,omitempty"`
	RejectionReason    sql.NullString        `db:"rejection_reason" json:"rejection_reason,omitempty"`
	CreatedAt          time.Time             `db:"created_at" json:"created_at"`
	UpdatedAt          time.Time             `db:"updated_at" json:"updated_at"`
}

// PayoutRequest captures the public request body for POST /payouts.
type PayoutRequest struct {
	AmountSats      int64                 `json:"amount_sats"`
	Destination     string                `json:"destination"`
	DestinationType PayoutDestinationType `json:"destination_type"`
	AutoTrigger     bool                  `json:"auto_trigger"`
}

// Payout-specific domain errors.
var (
	ErrPayoutNotFound       = NewDomainError("PAYOUT_NOT_FOUND", "Payout not found")
	ErrPayoutInvalidStatus  = NewDomainError("PAYOUT_INVALID_STATUS", "Payout state transition not allowed")
	ErrPayoutInvalidDest    = NewDomainError("INVALID_DESTINATION", "Invalid destination address or invoice")
	ErrPayoutAmountTooSmall = NewDomainError("AMOUNT_TOO_SMALL", "Amount below minimum")
	ErrPayoutForbidden      = NewDomainError("FORBIDDEN", "Not allowed to act on this payout")
)
