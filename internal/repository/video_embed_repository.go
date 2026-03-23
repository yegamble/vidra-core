package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"
)

type videoEmbedPrivacyRepository struct {
	db *sqlx.DB
}

// NewVideoEmbedPrivacyRepository creates a new VideoEmbedPrivacyRepository.
func NewVideoEmbedPrivacyRepository(db *sqlx.DB) port.VideoEmbedPrivacyRepository {
	return &videoEmbedPrivacyRepository{db: db}
}

func (r *videoEmbedPrivacyRepository) Get(ctx context.Context, videoID string) (*domain.VideoEmbedPrivacy, error) {
	var privacy domain.VideoEmbedPrivacy
	err := r.db.GetContext(ctx, &privacy,
		`SELECT video_id, status FROM video_embed_privacy WHERE video_id = $1`, videoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Default: embedding enabled
			return &domain.VideoEmbedPrivacy{
				VideoID:        videoID,
				Status:         domain.EmbedEnabled,
				AllowedDomains: []string{},
			}, nil
		}
		return nil, fmt.Errorf("get embed privacy: %w", err)
	}

	// Load allowed domains
	var domains []string
	err = r.db.SelectContext(ctx, &domains,
		`SELECT domain FROM video_embed_allowed_domains WHERE video_id = $1 ORDER BY id ASC`, videoID)
	if err != nil {
		return nil, fmt.Errorf("get embed allowed domains: %w", err)
	}
	if domains == nil {
		domains = []string{}
	}
	privacy.AllowedDomains = domains
	return &privacy, nil
}

func (r *videoEmbedPrivacyRepository) Upsert(ctx context.Context, privacy *domain.VideoEmbedPrivacy) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO video_embed_privacy (video_id, status) VALUES ($1, $2)
		 ON CONFLICT (video_id) DO UPDATE SET status = EXCLUDED.status`,
		privacy.VideoID, privacy.Status)
	if err != nil {
		return fmt.Errorf("upsert embed privacy: %w", err)
	}

	// Replace allowed domains
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM video_embed_allowed_domains WHERE video_id = $1`, privacy.VideoID); err != nil {
		return fmt.Errorf("delete embed allowed domains: %w", err)
	}
	for _, d := range privacy.AllowedDomains {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO video_embed_allowed_domains (video_id, domain) VALUES ($1, $2)`,
			privacy.VideoID, d); err != nil {
			return fmt.Errorf("insert embed allowed domain: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit embed privacy: %w", err)
	}
	return nil
}

func (r *videoEmbedPrivacyRepository) IsDomainAllowed(ctx context.Context, videoID string, domainName string) (bool, error) {
	var privacy domain.VideoEmbedPrivacy
	err := r.db.GetContext(ctx, &privacy,
		`SELECT video_id, status FROM video_embed_privacy WHERE video_id = $1`, videoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Default: embedding enabled
			return true, nil
		}
		return false, fmt.Errorf("get embed privacy: %w", err)
	}

	switch privacy.Status {
	case domain.EmbedEnabled:
		return true, nil
	case domain.EmbedDisabled:
		return false, nil
	case domain.EmbedWhitelist:
		var count int
		err := r.db.GetContext(ctx, &count,
			`SELECT COUNT(*) FROM video_embed_allowed_domains WHERE video_id = $1 AND domain = $2`,
			videoID, domainName)
		if err != nil {
			return false, fmt.Errorf("check embed allowed domain: %w", err)
		}
		return count > 0, nil
	default:
		return true, nil
	}
}
