// Package inner_circle holds repositories for Inner Circle aggregates: tiers,
// memberships, and channel posts.
package inner_circle

import (
	"context"
	"errors"
	"fmt"

	"vidra-core/internal/domain"
	icusecase "vidra-core/internal/usecase/inner_circle"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// ErrInvalidTierID is returned when a tier ID is not one of the canonical
// supporter/vip/elite values.
var ErrInvalidTierID = errors.New("inner_circle: invalid tier ID")

// TierUpsert is the canonical shape for a single-tier update — re-exported
// from the use-case package so callers can compose without circular imports.
type TierUpsert = icusecase.TierUpsertInput

// TierRepository reads and writes the inner_circle_tiers table.
type TierRepository struct {
	db *sqlx.DB
}

// NewTierRepository builds a repository over the given DB handle.
func NewTierRepository(db *sqlx.DB) *TierRepository {
	return &TierRepository{db: db}
}

// ListByChannel returns every tier for the channel ordered by tier rank
// (supporter → vip → elite). Member counts are joined from the memberships
// table for active rows whose expires_at is still in the future.
func (r *TierRepository) ListByChannel(ctx context.Context, channelID uuid.UUID) ([]domain.InnerCircleTierWithCount, error) {
	const query = `
		SELECT t.id, t.channel_id, t.tier_id, t.monthly_usd_cents, t.monthly_sats,
		       t.perks, t.enabled, t.created_at, t.updated_at,
		       COALESCE(m.member_count, 0) AS member_count
		FROM inner_circle_tiers t
		LEFT JOIN (
			SELECT channel_id, tier_id, COUNT(*)::int AS member_count
			FROM inner_circle_memberships
			WHERE status = 'active' AND expires_at > NOW()
			GROUP BY channel_id, tier_id
		) m ON m.channel_id = t.channel_id AND m.tier_id = t.tier_id
		WHERE t.channel_id = $1
		ORDER BY CASE t.tier_id
			WHEN 'supporter' THEN 1
			WHEN 'vip' THEN 2
			WHEN 'elite' THEN 3
			ELSE 99
		END
	`
	rows, err := r.db.QueryxContext(ctx, query, channelID)
	if err != nil {
		return nil, fmt.Errorf("inner_circle: list tiers: %w", err)
	}
	defer rows.Close()

	out := make([]domain.InnerCircleTierWithCount, 0, 3)
	for rows.Next() {
		var t domain.InnerCircleTierWithCount
		var perks pq.StringArray
		if err := rows.Scan(
			&t.ID, &t.ChannelID, &t.TierID, &t.MonthlyUSDCents, &t.MonthlySats,
			&perks, &t.Enabled, &t.CreatedAt, &t.UpdatedAt, &t.MemberCount,
		); err != nil {
			return nil, fmt.Errorf("inner_circle: scan tier: %w", err)
		}
		t.Perks = []string(perks)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inner_circle: iterate tiers: %w", err)
	}
	return out, nil
}

// UpsertAll updates the three canonical tiers for the channel in one
// transaction. Rejects unknown tier IDs and durations beyond [0, math.MaxInt).
// Missing tiers are inserted; existing tiers have all four fields overwritten.
func (r *TierRepository) UpsertAll(ctx context.Context, channelID uuid.UUID, items []icusecase.TierUpsertInput) error {
	if len(items) == 0 {
		return fmt.Errorf("inner_circle: upsert requires at least one tier")
	}
	for _, it := range items {
		if !icusecase.ValidTierID(it.TierID) {
			return fmt.Errorf("%w: %q", ErrInvalidTierID, it.TierID)
		}
		if it.MonthlyUSDCents < 0 {
			return fmt.Errorf("inner_circle: monthly_usd_cents must be >= 0 (got %d)", it.MonthlyUSDCents)
		}
		if it.MonthlySats < 0 {
			return fmt.Errorf("inner_circle: monthly_sats must be >= 0 (got %d)", it.MonthlySats)
		}
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("inner_circle: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const query = `
		INSERT INTO inner_circle_tiers
			(channel_id, tier_id, monthly_usd_cents, monthly_sats, perks, enabled)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (channel_id, tier_id) DO UPDATE SET
			monthly_usd_cents = EXCLUDED.monthly_usd_cents,
			monthly_sats      = EXCLUDED.monthly_sats,
			perks             = EXCLUDED.perks,
			enabled           = EXCLUDED.enabled,
			updated_at        = NOW()
	`
	for _, it := range items {
		if _, err := tx.ExecContext(ctx, query,
			channelID, it.TierID, it.MonthlyUSDCents, it.MonthlySats,
			pq.Array(it.Perks), it.Enabled,
		); err != nil {
			return fmt.Errorf("inner_circle: upsert tier %s: %w", it.TierID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("inner_circle: commit tier upsert: %w", err)
	}
	return nil
}

// SeedDefaults inserts default tier rows for the channel if none exist.
// Used by the channel-creation flow so new channels can be subscribed to
// without needing the creator to visit /studio/inner-circle first. Idempotent.
func (r *TierRepository) SeedDefaults(ctx context.Context, channelID uuid.UUID) error {
	defaults := []icusecase.TierUpsertInput{
		{TierID: "supporter", MonthlyUSDCents: 299, MonthlySats: 8500, Enabled: true,
			Perks: []string{"Supporter badge in comments", "Access to members-only posts"}},
		{TierID: "vip", MonthlyUSDCents: 799, MonthlySats: 22750, Enabled: true,
			Perks: []string{"Everything in Supporter", "Exclusive VIP badge", "Early access to new videos"}},
		{TierID: "elite", MonthlyUSDCents: 1999, MonthlySats: 56950, Enabled: true,
			Perks: []string{"Everything in VIP", "Shoutout in videos", "Direct message access"}},
	}
	const query = `
		INSERT INTO inner_circle_tiers
			(channel_id, tier_id, monthly_usd_cents, monthly_sats, perks, enabled)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (channel_id, tier_id) DO NOTHING
	`
	for _, d := range defaults {
		if _, err := r.db.ExecContext(ctx, query,
			channelID, d.TierID, d.MonthlyUSDCents, d.MonthlySats, pq.Array(d.Perks), d.Enabled,
		); err != nil {
			return fmt.Errorf("inner_circle: seed default %s: %w", d.TierID, err)
		}
	}
	return nil
}
