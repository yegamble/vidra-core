package migration_etl

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

// ReverseETLService syncs Vidra Core data back to a PeerTube schema for rollback.
// Only handles core entities (users, channels, videos, comments). Vidra-only data
// (payments, ATProto, IPFS metadata) is acknowledged as lossy on rollback.
type ReverseETLService struct {
	idMappingRepo port.IDMappingRepository
}

// NewReverseETLService creates a new ReverseETLService.
func NewReverseETLService(idMappingRepo port.IDMappingRepository) *ReverseETLService {
	return &ReverseETLService{
		idMappingRepo: idMappingRepo,
	}
}

// ShouldSync returns true if the entity was created after the migration started
// and therefore needs to be synced back to PeerTube on rollback.
func (r *ReverseETLService) ShouldSync(entityCreatedAt, migrationStartedAt time.Time) bool {
	return entityCreatedAt.After(migrationStartedAt)
}

// ReverseSync syncs new Vidra Core entities back to the PeerTube source database.
// Connects to the PeerTube DB using the same credentials as the forward ETL.
// NOTE: PeerTube DB credentials must have WRITE access.
func (r *ReverseETLService) ReverseSync(ctx context.Context, job *domain.MigrationJob, users []*domain.User, videos []*domain.Video, comments []*domain.Comment) (*ReverseSyncStats, error) {
	if job.StartedAt == nil {
		return nil, fmt.Errorf("migration job has no start time, cannot determine sync window")
	}

	sourceDB, err := connectSourceDB(job)
	if err != nil {
		return nil, fmt.Errorf("connecting to peertube source db for reverse sync: %w", err)
	}
	defer sourceDB.Close()

	stats := &ReverseSyncStats{}

	for _, user := range users {
		if !r.ShouldSync(user.CreatedAt, *job.StartedAt) {
			continue
		}
		if err := r.syncUser(ctx, sourceDB, job.ID, user); err != nil {
			stats.UsersFailed++
			slog.Info(fmt.Sprintf("reverse etl: failed to sync user %s: %v", user.ID, err))
			continue
		}
		stats.UsersSynced++
	}

	for _, video := range videos {
		if !r.ShouldSync(video.CreatedAt, *job.StartedAt) {
			continue
		}
		if err := r.syncVideo(ctx, sourceDB, job.ID, video); err != nil {
			stats.VideosFailed++
			slog.Info(fmt.Sprintf("reverse etl: failed to sync video %s: %v", video.ID, err))
			continue
		}
		stats.VideosSynced++
	}

	for _, comment := range comments {
		if !r.ShouldSync(comment.CreatedAt, *job.StartedAt) {
			continue
		}
		if err := r.syncComment(ctx, sourceDB, job.ID, comment); err != nil {
			stats.CommentsFailed++
			slog.Info(fmt.Sprintf("reverse etl: failed to sync comment %s: %v", comment.ID, err))
			continue
		}
		stats.CommentsSynced++
	}

	return stats, nil
}

// syncUser inserts or updates a user in the PeerTube database.
func (r *ReverseETLService) syncUser(ctx context.Context, ptDB *sqlx.DB, jobID string, user *domain.User) error {
	// Check if this user has an existing PeerTube ID
	ptID, err := r.idMappingRepo.GetPeertubeID(ctx, "user", user.ID)
	if err == nil {
		// Update existing PeerTube user (including role and blocked status)
		_, updateErr := ptDB.ExecContext(ctx,
			`UPDATE "user" SET username = $1, email = $2, role = $3, blocked = $4 WHERE id = $5`,
			user.Username, user.Email, reverseMapRole(user.Role), !user.IsActive, ptID,
		)
		return updateErr
	}

	// New user: INSERT without specifying ID, let PostgreSQL auto-increment
	var newPTID int
	insertErr := ptDB.QueryRowContext(ctx,
		`INSERT INTO "user" (username, email, role, blocked, "createdAt") VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		user.Username, user.Email, reverseMapRole(user.Role), !user.IsActive, user.CreatedAt,
	).Scan(&newPTID)
	if insertErr != nil {
		return fmt.Errorf("inserting new user into peertube: %w", insertErr)
	}

	// Store the reverse mapping
	return r.idMappingRepo.Upsert(ctx, &domain.MigrationIDMapping{
		JobID:      jobID,
		EntityType: "user",
		PeertubeID: newPTID,
		VidraID:    user.ID,
	})
}

// syncVideo inserts or updates a video in the PeerTube database.
func (r *ReverseETLService) syncVideo(ctx context.Context, ptDB *sqlx.DB, jobID string, video *domain.Video) error {
	ptID, err := r.idMappingRepo.GetPeertubeID(ctx, "video", video.ID)
	if err == nil {
		_, updateErr := ptDB.ExecContext(ctx,
			`UPDATE video SET name = $1, description = $2 WHERE id = $3`,
			video.Title, video.Description, ptID,
		)
		return updateErr
	}

	var newPTID int
	insertErr := ptDB.QueryRowContext(ctx,
		`INSERT INTO video (name, description, privacy, duration, "createdAt") VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		video.Title, video.Description, reverseMapPrivacy(video.Privacy), video.Duration, video.CreatedAt,
	).Scan(&newPTID)
	if insertErr != nil {
		return fmt.Errorf("inserting new video into peertube: %w", insertErr)
	}

	return r.idMappingRepo.Upsert(ctx, &domain.MigrationIDMapping{
		JobID:      jobID,
		EntityType: "video",
		PeertubeID: newPTID,
		VidraID:    video.ID,
	})
}

// syncComment inserts or updates a comment in the PeerTube database.
func (r *ReverseETLService) syncComment(ctx context.Context, ptDB *sqlx.DB, jobID string, comment *domain.Comment) error {
	ptID, err := r.idMappingRepo.GetPeertubeID(ctx, "comment", comment.ID.String())
	if err == nil {
		_, updateErr := ptDB.ExecContext(ctx,
			`UPDATE "videoComment" SET text = $1 WHERE id = $2`,
			comment.Body, ptID,
		)
		return updateErr
	}

	// Resolve video PeerTube ID
	videoPTID, videoErr := r.idMappingRepo.GetPeertubeID(ctx, "video", comment.VideoID.String())
	if videoErr != nil {
		return fmt.Errorf("video %s has no peertube mapping for comment reverse sync", comment.VideoID)
	}

	var newPTID int
	insertErr := ptDB.QueryRowContext(ctx,
		`INSERT INTO "videoComment" (text, "videoId", "createdAt") VALUES ($1, $2, $3) RETURNING id`,
		comment.Body, videoPTID, comment.CreatedAt,
	).Scan(&newPTID)
	if insertErr != nil {
		return fmt.Errorf("inserting new comment into peertube: %w", insertErr)
	}

	return r.idMappingRepo.Upsert(ctx, &domain.MigrationIDMapping{
		JobID:      jobID,
		EntityType: "comment",
		PeertubeID: newPTID,
		VidraID:    comment.ID.String(),
	})
}

// ReverseSyncStats tracks the results of a reverse sync operation.
type ReverseSyncStats struct {
	UsersSynced    int `json:"users_synced"`
	UsersFailed    int `json:"users_failed"`
	VideosSynced   int `json:"videos_synced"`
	VideosFailed   int `json:"videos_failed"`
	CommentsSynced int `json:"comments_synced"`
	CommentsFailed int `json:"comments_failed"`
}

// reverseMapRole converts Vidra Core role to PeerTube integer role.
func reverseMapRole(role domain.UserRole) int {
	switch role {
	case domain.RoleAdmin:
		return 0
	case domain.RoleMod:
		return 1
	default:
		return 2 // user
	}
}

// reverseMapPrivacy converts Vidra Core privacy to PeerTube integer privacy.
func reverseMapPrivacy(privacy domain.Privacy) int {
	switch privacy {
	case domain.PrivacyPublic:
		return 1
	case domain.PrivacyUnlisted:
		return 2
	default:
		return 3 // private
	}
}
