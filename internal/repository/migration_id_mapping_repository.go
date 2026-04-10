package repository

import (
	"context"
	"database/sql"
	"fmt"

	"vidra-core/internal/domain"

	"github.com/jmoiron/sqlx"
)

// MigrationIDMappingRepository implements port.IDMappingRepository using SQLX.
type MigrationIDMappingRepository struct {
	db *sqlx.DB
}

// NewMigrationIDMappingRepository creates a new MigrationIDMappingRepository.
func NewMigrationIDMappingRepository(db *sqlx.DB) *MigrationIDMappingRepository {
	return &MigrationIDMappingRepository{db: db}
}

// Upsert inserts or updates a PeerTube-to-Vidra ID mapping (idempotent for resume).
func (r *MigrationIDMappingRepository) Upsert(ctx context.Context, mapping *domain.MigrationIDMapping) error {
	query := `
		INSERT INTO migration_id_mappings (job_id, entity_type, peertube_id, vidra_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (job_id, entity_type, peertube_id) DO UPDATE
		SET vidra_id = EXCLUDED.vidra_id`

	_, err := r.db.ExecContext(ctx, query, mapping.JobID, mapping.EntityType, mapping.PeertubeID, mapping.VidraID)
	if err != nil {
		return fmt.Errorf("upserting id mapping: %w", err)
	}
	return nil
}

// GetVidraID looks up a Vidra Core ID by PeerTube entity type and integer ID.
func (r *MigrationIDMappingRepository) GetVidraID(ctx context.Context, entityType string, peertubeID int) (string, error) {
	var vidraID string
	query := `SELECT vidra_id FROM migration_id_mappings WHERE entity_type = $1 AND peertube_id = $2`
	err := r.db.QueryRowContext(ctx, query, entityType, peertubeID).Scan(&vidraID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("id mapping not found: %w", domain.ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("getting vidra id: %w", err)
	}
	return vidraID, nil
}

// GetPeertubeID looks up a PeerTube integer ID by Vidra Core entity type and ID.
func (r *MigrationIDMappingRepository) GetPeertubeID(ctx context.Context, entityType string, vidraID string) (int, error) {
	var peertubeID int
	query := `SELECT peertube_id FROM migration_id_mappings WHERE entity_type = $1 AND vidra_id = $2`
	err := r.db.QueryRowContext(ctx, query, entityType, vidraID).Scan(&peertubeID)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("id mapping not found: %w", domain.ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("getting peertube id: %w", err)
	}
	return peertubeID, nil
}

// ListByJobID returns all ID mappings for a given migration job.
func (r *MigrationIDMappingRepository) ListByJobID(ctx context.Context, jobID string) ([]*domain.MigrationIDMapping, error) {
	var mappings []*domain.MigrationIDMapping
	query := `SELECT job_id, entity_type, peertube_id, vidra_id, created_at FROM migration_id_mappings WHERE job_id = $1 ORDER BY entity_type, peertube_id`
	err := r.db.SelectContext(ctx, &mappings, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("listing id mappings for job %s: %w", jobID, err)
	}
	return mappings, nil
}

// UpsertCheckpoint records that an ETL phase completed for a job.
func (r *MigrationIDMappingRepository) UpsertCheckpoint(ctx context.Context, jobID string, entityType string) error {
	query := `
		INSERT INTO migration_checkpoints (job_id, entity_type, completed_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (job_id, entity_type) DO UPDATE
		SET completed_at = NOW()`

	_, err := r.db.ExecContext(ctx, query, jobID, entityType)
	if err != nil {
		return fmt.Errorf("upserting checkpoint: %w", err)
	}
	return nil
}

// GetCompletedPhases returns the entity types that have completed for a job.
func (r *MigrationIDMappingRepository) GetCompletedPhases(ctx context.Context, jobID string) ([]string, error) {
	var phases []string
	query := `SELECT entity_type FROM migration_checkpoints WHERE job_id = $1 ORDER BY completed_at`
	err := r.db.SelectContext(ctx, &phases, query, jobID)
	if err != nil {
		return nil, fmt.Errorf("getting completed phases: %w", err)
	}
	return phases, nil
}
