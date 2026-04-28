package inner_circle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
)

// ErrTierNotFound is returned when subscribe targets a tier that does not exist
// for the channel (or is disabled).
var ErrTierNotFound = errors.New("inner_circle: tier not found or disabled")

// ErrInvalidStatus is returned when a state transition is invalid (e.g.
// cancelling an already-cancelled membership).
var ErrInvalidStatus = errors.New("inner_circle: invalid membership status for this operation")

// MembershipRepo is the subset of the membership repository the service uses.
type MembershipRepo interface {
	GetActiveTier(ctx context.Context, userID, channelID uuid.UUID) (string, error)
	ListMine(ctx context.Context, userID uuid.UUID, includePending bool) ([]domain.InnerCircleMembership, error)
	ListByChannel(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]domain.InnerCircleMembership, error)
	CreatePending(ctx context.Context, userID, channelID uuid.UUID, tierID string, polarSubscriptionID *string, btcpayInvoiceID *uuid.UUID, expiresAt time.Time) (uuid.UUID, error)
	Cancel(ctx context.Context, id, callerUserID uuid.UUID) error
	ExpireDue(ctx context.Context, pendingTimeout time.Duration) (activeExpired, pendingExpired int, err error)
}

// BTCPayInvoiceCreator is the subset of BTCPayService the membership service
// uses. Inputs match BTCPayService.CreateInvoice's positional contract.
type BTCPayInvoiceCreator interface {
	CreateInvoice(ctx context.Context, userID string, amountSats int64, currency string, paymentMethod string, metadata map[string]interface{}) (*domain.BTCPayInvoice, error)
}

// MembershipService coordinates subscribe / cancel / list operations.
type MembershipService struct {
	memberships MembershipRepo
	tiers       TierRepo
	channels    ChannelLookup
	btcpay      BTCPayInvoiceCreator
}

// NewMembershipService wires the service.
func NewMembershipService(memberships MembershipRepo, tiers TierRepo, channels ChannelLookup, btcpay BTCPayInvoiceCreator) *MembershipService {
	return &MembershipService{memberships: memberships, tiers: tiers, channels: channels, btcpay: btcpay}
}

// SubscribeBTCPayResult is the discriminated response returned to the
// frontend when a BTCPay invoice is created for an Inner Circle subscribe.
type SubscribeBTCPayResult struct {
	Kind         string                  `json:"kind"`
	Invoice      *domain.BTCPayInvoice   `json:"invoice"`
	MembershipID uuid.UUID               `json:"membership_id"`
}

// SubscribeBTCPay creates a BTCPay invoice for the requested tier and writes a
// pending membership row. The BTCPay webhook (T4) will flip pending → active
// on settlement.
func (s *MembershipService) SubscribeBTCPay(ctx context.Context, userID, channelID uuid.UUID, tierID string) (*SubscribeBTCPayResult, error) {
	if !ValidTierID(tierID) {
		return nil, fmt.Errorf("inner_circle: invalid tier id %q", tierID)
	}
	if _, err := s.channels.GetByID(ctx, channelID); err != nil {
		return nil, ErrChannelNotFound
	}
	tiers, err := s.tiers.ListByChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	var match *domain.InnerCircleTierWithCount
	for i := range tiers {
		if tiers[i].TierID == tierID && tiers[i].Enabled {
			match = &tiers[i]
			break
		}
	}
	if match == nil {
		return nil, ErrTierNotFound
	}

	if s.btcpay == nil {
		return nil, fmt.Errorf("inner_circle: btcpay service not configured")
	}
	invoice, err := s.btcpay.CreateInvoice(ctx, userID.String(), match.MonthlySats, "BTC", "both", map[string]interface{}{
		"type":       "inner_circle",
		"channel_id": channelID.String(),
		"tier_id":    tierID,
		"user_id":    userID.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("inner_circle: create invoice: %w", err)
	}

	invoiceUUID, parseErr := uuid.Parse(invoice.ID)
	var btcpayInvoiceID *uuid.UUID
	if parseErr == nil {
		btcpayInvoiceID = &invoiceUUID
	}
	pendingExpiry := time.Now().UTC().Add(time.Hour) // matches expiry job sweep interval
	id, err := s.memberships.CreatePending(ctx, userID, channelID, tierID, nil, btcpayInvoiceID, pendingExpiry)
	if err != nil {
		return nil, fmt.Errorf("inner_circle: persist pending membership: %w", err)
	}
	return &SubscribeBTCPayResult{Kind: "btcpay", Invoice: invoice, MembershipID: id}, nil
}

// CreatePendingPolar writes a pending membership row immediately when the user
// opens the Polar checkout in vidra-user. The Polar webhook (T5) flips it to
// active on subscription.created/updated. polarSessionID is stored opaquely so
// the frontend can correlate (the actual subscription_id arrives via webhook).
func (s *MembershipService) CreatePendingPolar(ctx context.Context, userID, channelID uuid.UUID, tierID, polarSessionID string) (uuid.UUID, error) {
	if !ValidTierID(tierID) {
		return uuid.Nil, fmt.Errorf("inner_circle: invalid tier id %q", tierID)
	}
	if _, err := s.channels.GetByID(ctx, channelID); err != nil {
		return uuid.Nil, ErrChannelNotFound
	}
	pendingExpiry := time.Now().UTC().Add(time.Hour)
	var sessionPtr *string
	if polarSessionID != "" {
		sessionPtr = &polarSessionID
	}
	return s.memberships.CreatePending(ctx, userID, channelID, tierID, sessionPtr, nil, pendingExpiry)
}

// ErrMembershipNotFound is the sentinel returned when CancelMine targets a
// membership that doesn't exist or isn't owned by the caller. Any other error
// (DB timeout, deadlock) is wrapped and propagated so handlers can map to 5xx.
var ErrMembershipNotFound = errors.New("inner_circle: membership not found or not yours")

// CancelMine cancels a membership the caller owns. Status flips to cancelled;
// expires_at is left as-is so the user retains access until the period ends.
// For Polar memberships this only flips local state; the actual Polar
// subscription cancel is invoked by vidra-user (single Polar caller).
//
// Returns ErrMembershipNotFound if no row matched the (id, user_id) pair.
// Returns wrapped errors for any other failure (DB timeout, etc).
func (s *MembershipService) CancelMine(ctx context.Context, userID, membershipID uuid.UUID) error {
	if err := s.memberships.Cancel(ctx, membershipID, userID); err != nil {
		// The repo returns icrepo.ErrMembershipNotFound for the no-row case.
		if err.Error() == "inner_circle: membership not found" {
			return ErrMembershipNotFound
		}
		return err
	}
	return nil
}

// ListMine returns all of the caller's memberships. includePending controls
// whether pending rows (not yet activated by webhook) are surfaced.
func (s *MembershipService) ListMine(ctx context.Context, userID uuid.UUID, includePending bool) ([]domain.InnerCircleMembership, error) {
	return s.memberships.ListMine(ctx, userID, includePending)
}

// ListByChannel returns paginated active members for the channel. Caller must
// be the channel owner.
func (s *MembershipService) ListByChannel(ctx context.Context, channelID, callerUserID uuid.UUID, limit, offset int) ([]domain.InnerCircleMembership, error) {
	channel, err := s.channels.GetByID(ctx, channelID)
	if err != nil {
		return nil, ErrChannelNotFound
	}
	if channel.AccountID != callerUserID {
		return nil, ErrNotChannelOwner
	}
	return s.memberships.ListByChannel(ctx, channelID, limit, offset)
}

// RunExpiry is invoked by the dedicated scheduler. Returns the count expired
// in each bucket. The bootstrapper wraps this in a goroutine + ticker.
func (s *MembershipService) RunExpiry(ctx context.Context, pendingTimeout time.Duration) (activeExpired, pendingExpired int, err error) {
	return s.memberships.ExpireDue(ctx, pendingTimeout)
}
