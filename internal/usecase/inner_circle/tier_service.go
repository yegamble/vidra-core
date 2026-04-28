package inner_circle

import (
	"context"
	"errors"
	"fmt"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
)

// ErrChannelNotFound is returned when a tier operation targets a channel that
// does not exist.
var ErrChannelNotFound = errors.New("inner_circle: channel not found")

// ErrNotChannelOwner is returned when a creator-only operation is attempted by
// a user who does not own the channel.
var ErrNotChannelOwner = errors.New("inner_circle: caller does not own channel")

// ErrTooManyTiers is returned when an UpsertAll request contains more than the
// canonical 3 tiers, or duplicates the same tier_id twice.
var ErrTooManyTiers = errors.New("inner_circle: at most 3 tiers, no duplicates")

// ErrPerksTooLarge is returned when a tier perk list exceeds the limits.
var ErrPerksTooLarge = errors.New("inner_circle: perks list too large")

const (
	maxPerksPerTier = 10
	maxPerkLength   = 80
)

// TierUpsertInput is the canonical shape for a single-tier write, shared by the
// service interface and the repository.
type TierUpsertInput struct {
	TierID          string
	MonthlyUSDCents int
	MonthlySats     int64
	Perks           []string
	Enabled         bool
}

// TierRepo is the subset of the tier repository the service depends on.
// Defined here so handlers/tests can mock it without dragging the concrete
// repository in.
type TierRepo interface {
	ListByChannel(ctx context.Context, channelID uuid.UUID) ([]domain.InnerCircleTierWithCount, error)
	UpsertAll(ctx context.Context, channelID uuid.UUID, items []TierUpsertInput) error
}

// ChannelLookup is the subset of channel access the tier service needs.
type ChannelLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error)
}

// TierService coordinates tier reads + writes with channel ownership checks.
type TierService struct {
	repo     TierRepo
	channels ChannelLookup
}

// NewTierService wires the tier service.
func NewTierService(repo TierRepo, channels ChannelLookup) *TierService {
	return &TierService{repo: repo, channels: channels}
}

// List returns every tier for the channel, with active member counts. Public.
func (s *TierService) List(ctx context.Context, channelID uuid.UUID) ([]domain.InnerCircleTierWithCount, error) {
	if _, err := s.channels.GetByID(ctx, channelID); err != nil {
		return nil, ErrChannelNotFound
	}
	return s.repo.ListByChannel(ctx, channelID)
}

// Update overwrites the per-tier configuration for the given channel. Caller
// must be the channel owner. Validates tier IDs, perk list size, and price
// non-negativity. Bulk: caller passes 1-3 tiers; missing tiers retain their
// existing values.
func (s *TierService) Update(ctx context.Context, channelID, callerUserID uuid.UUID, items []TierUpsertInput) error {
	channel, err := s.channels.GetByID(ctx, channelID)
	if err != nil {
		return ErrChannelNotFound
	}
	if channel.AccountID != callerUserID {
		return ErrNotChannelOwner
	}
	if len(items) == 0 || len(items) > 3 {
		return ErrTooManyTiers
	}
	seen := make(map[string]struct{}, len(items))
	for _, it := range items {
		if !ValidTierID(it.TierID) {
			return fmt.Errorf("inner_circle: invalid tier_id %q", it.TierID)
		}
		if _, dup := seen[it.TierID]; dup {
			return ErrTooManyTiers
		}
		seen[it.TierID] = struct{}{}
		if len(it.Perks) > maxPerksPerTier {
			return ErrPerksTooLarge
		}
		for _, p := range it.Perks {
			if len(p) > maxPerkLength {
				return ErrPerksTooLarge
			}
		}
		if it.MonthlyUSDCents < 0 || it.MonthlySats < 0 {
			return fmt.Errorf("inner_circle: tier %s prices must be >= 0", it.TierID)
		}
	}
	return s.repo.UpsertAll(ctx, channelID, items)
}

// Note: ListByChannel and Update both load the channel first to verify
// existence (and ownership for Update). The lookup interface keeps the service
// pure — handlers can stub it in unit tests without a real DB.
