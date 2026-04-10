package migration_etl

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"
)

// idMap tracks PeerTube integer IDs → Vidra Core UUIDs across extraction phases.
type idMap struct {
	users    map[int]string    // PT user id → Vidra Core user ID (string)
	channels map[int]uuid.UUID // PT channel id → Vidra Core channel UUID
	videos   map[int]string    // PT video id → Vidra Core video ID (string)
	comments map[int]uuid.UUID // PT comment id → Vidra Core comment UUID
}

func newIDMap() *idMap {
	return &idMap{
		users:    make(map[int]string),
		channels: make(map[int]uuid.UUID),
		videos:   make(map[int]string),
		comments: make(map[int]uuid.UUID),
	}
}

// channelOwner stores channel → user ownership for resolving video user_id.
type channelOwner struct {
	userID    string
	channelID uuid.UUID
}

// ETLService handles PeerTube instance migration operations.
type ETLService struct {
	repo          port.MigrationJobRepository
	userRepo      port.UserRepository
	channelRepo   port.ChannelRepository
	commentRepo   port.CommentRepository
	playlistRepo  port.PlaylistRepository
	captionRepo   port.CaptionRepository
	videoRepo     port.VideoRepository
	idMappingRepo port.IDMappingRepository
}

// NewETLService creates a new ETLService with all target repositories.
func NewETLService(
	repo port.MigrationJobRepository,
	userRepo port.UserRepository,
	channelRepo port.ChannelRepository,
	commentRepo port.CommentRepository,
	playlistRepo port.PlaylistRepository,
	captionRepo port.CaptionRepository,
	videoRepo port.VideoRepository,
	idMappingRepo port.IDMappingRepository,
) *ETLService {
	return &ETLService{
		repo:          repo,
		userRepo:      userRepo,
		channelRepo:   channelRepo,
		commentRepo:   commentRepo,
		playlistRepo:  playlistRepo,
		captionRepo:   captionRepo,
		videoRepo:     videoRepo,
		idMappingRepo: idMappingRepo,
	}
}

// connectSourceDB opens a connection to the PeerTube source database using job fields.
func connectSourceDB(job *domain.MigrationJob) (*sqlx.DB, error) {
	if job.SourceDBHost == nil || job.SourceDBName == nil || job.SourceDBUser == nil || job.SourceDBPassword == nil {
		return nil, fmt.Errorf("source database connection details incomplete")
	}
	dbPort := 5432
	if job.SourceDBPort != nil {
		dbPort = *job.SourceDBPort
	}
	dsn := fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=disable",
		*job.SourceDBHost, dbPort, *job.SourceDBName, *job.SourceDBUser, *job.SourceDBPassword)

	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("connecting to source database: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	return db, nil
}

// ---------------------------------------------------------------------------
// Public API (unchanged signatures except constructor)
// ---------------------------------------------------------------------------

// StartMigration creates and starts a new migration job.
func (s *ETLService) StartMigration(ctx context.Context, adminUserID string, req *domain.MigrationRequest) (*domain.MigrationJob, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrValidation, err.Error())
	}

	running, err := s.repo.GetRunning(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking for running migration: %w", err)
	}
	if running != nil {
		return nil, domain.ErrMigrationInProgress
	}

	dbPort := req.SourceDBPort
	if dbPort <= 0 {
		dbPort = 5432
	}

	emptyStats := &domain.MigrationStats{}
	statsJSON, err := json.Marshal(emptyStats)
	if err != nil {
		return nil, fmt.Errorf("marshaling initial stats: %w", err)
	}

	job := &domain.MigrationJob{
		AdminUserID:      adminUserID,
		SourceHost:       req.SourceHost,
		Status:           domain.MigrationStatusPending,
		DryRun:           false,
		StatsJSON:        statsJSON,
		SourceDBHost:     &req.SourceDBHost,
		SourceDBPort:     &dbPort,
		SourceDBName:     &req.SourceDBName,
		SourceDBUser:     &req.SourceDBUser,
		SourceDBPassword: &req.SourceDBPassword,
	}

	if req.SourceMediaPath != "" {
		job.SourceMediaPath = &req.SourceMediaPath
	}

	if err := s.repo.Create(ctx, job); err != nil {
		return nil, fmt.Errorf("creating migration job: %w", err)
	}

	go s.runPipeline(job.ID, adminUserID)

	return job, nil
}

// GetMigrationStatus returns a migration job by ID.
func (s *ETLService) GetMigrationStatus(ctx context.Context, id string) (*domain.MigrationJob, error) {
	job, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting migration job: %w", err)
	}
	return job, nil
}

// ListMigrations lists migration jobs with pagination.
func (s *ETLService) ListMigrations(ctx context.Context, limit, offset int) ([]*domain.MigrationJob, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	jobs, total, err := s.repo.List(ctx, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing migration jobs: %w", err)
	}
	return jobs, total, nil
}

// CancelMigration cancels a migration job.
func (s *ETLService) CancelMigration(ctx context.Context, id string) error {
	job, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("getting migration job for cancellation: %w", err)
	}

	if job.Status.IsTerminal() {
		return domain.ErrMigrationCantCancel
	}

	if !job.CanTransition(domain.MigrationStatusCancelled) {
		return domain.ErrMigrationCantCancel
	}

	job.Status = domain.MigrationStatusCancelled
	now := time.Now()
	job.CompletedAt = &now

	if err := s.repo.Update(ctx, job); err != nil {
		return fmt.Errorf("cancelling migration job: %w", err)
	}

	return nil
}

// DryRun creates and runs a dry-run migration (no data written).
func (s *ETLService) DryRun(ctx context.Context, adminUserID string, req *domain.MigrationRequest) (*domain.MigrationJob, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrValidation, err.Error())
	}

	dbPort := req.SourceDBPort
	if dbPort <= 0 {
		dbPort = 5432
	}

	emptyStats := &domain.MigrationStats{}
	statsJSON, err := json.Marshal(emptyStats)
	if err != nil {
		return nil, fmt.Errorf("marshaling initial stats: %w", err)
	}

	job := &domain.MigrationJob{
		AdminUserID:      adminUserID,
		SourceHost:       req.SourceHost,
		Status:           domain.MigrationStatusPending,
		DryRun:           true,
		StatsJSON:        statsJSON,
		SourceDBHost:     &req.SourceDBHost,
		SourceDBPort:     &dbPort,
		SourceDBName:     &req.SourceDBName,
		SourceDBUser:     &req.SourceDBUser,
		SourceDBPassword: &req.SourceDBPassword,
	}

	if req.SourceMediaPath != "" {
		job.SourceMediaPath = &req.SourceMediaPath
	}

	if err := s.repo.Create(ctx, job); err != nil {
		return nil, fmt.Errorf("creating dry-run migration job: %w", err)
	}

	go s.runDryRunPipeline(job.ID)

	return job, nil
}

// ResumeMigration resumes a failed migration from its last completed checkpoint.
func (s *ETLService) ResumeMigration(ctx context.Context, jobID string) (*domain.MigrationJob, error) {
	job, err := s.repo.GetByID(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("getting migration job for resume: %w", err)
	}

	if job.Status != domain.MigrationStatusFailed {
		return nil, domain.ErrMigrationCantResume
	}

	if !job.CanTransition(domain.MigrationStatusResuming) {
		return nil, domain.ErrMigrationCantResume
	}

	job.Status = domain.MigrationStatusResuming
	job.ErrorMessage = nil
	if err := s.repo.Update(ctx, job); err != nil {
		return nil, fmt.Errorf("updating job to resuming: %w", err)
	}

	go s.runResumePipeline(job.ID, job.AdminUserID)

	return job, nil
}

// rebuildIDMap reconstructs the in-memory ID map from persisted DB mappings.
func (s *ETLService) rebuildIDMap(ctx context.Context, jobID string) (*idMap, error) {
	mappings, err := s.idMappingRepo.ListByJobID(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("loading id mappings for rebuild: %w", err)
	}

	ids := newIDMap()
	for _, m := range mappings {
		switch m.EntityType {
		case "user":
			ids.users[m.PeertubeID] = m.VidraID
		case "channel":
			parsed, parseErr := uuid.Parse(m.VidraID)
			if parseErr == nil {
				ids.channels[m.PeertubeID] = parsed
			}
		case "video":
			ids.videos[m.PeertubeID] = m.VidraID
		case "comment":
			parsed, parseErr := uuid.Parse(m.VidraID)
			if parseErr == nil {
				ids.comments[m.PeertubeID] = parsed
			}
		}
	}
	return ids, nil
}

// runResumePipeline continues a failed pipeline from the last checkpoint.
func (s *ETLService) runResumePipeline(jobID, adminUserID string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Info(fmt.Sprintf("migration resume pipeline: recovered panic in job %s: %v", jobID, r))
		}
	}()

	ctx := context.Background()

	job, err := s.repo.GetByID(ctx, jobID)
	if err != nil {
		slog.Info(fmt.Sprintf("resume pipeline: failed to load job %s: %v", jobID, err))
		return
	}

	job.Status = domain.MigrationStatusRunning
	if err := s.repo.Update(ctx, job); err != nil {
		slog.Info(fmt.Sprintf("resume pipeline: failed to update job %s to running: %v", jobID, err))
		return
	}

	sourceDB, err := connectSourceDB(job)
	if err != nil {
		s.failJob(ctx, job, fmt.Sprintf("source db connection failed on resume: %v", err))
		return
	}
	defer sourceDB.Close()

	// Load completed phases
	completedPhases, err := s.idMappingRepo.GetCompletedPhases(ctx, jobID)
	if err != nil {
		s.failJob(ctx, job, fmt.Sprintf("failed to load checkpoints: %v", err))
		return
	}
	completed := make(map[string]bool)
	for _, phase := range completedPhases {
		completed[phase] = true
	}

	// Rebuild in-memory ID map from persisted mappings
	ids, err := s.rebuildIDMap(ctx, jobID)
	if err != nil {
		s.failJob(ctx, job, fmt.Sprintf("failed to rebuild id map: %v", err))
		return
	}

	stats, _ := job.GetStats()
	if stats == nil {
		stats = &domain.MigrationStats{}
	}

	channelOwners := make(map[int]*channelOwner)

	// Run only phases that haven't completed
	if !completed["users"] {
		s.extractUsers(ctx, sourceDB, job, stats, ids)
		if err := s.idMappingRepo.UpsertCheckpoint(ctx, jobID, "users"); err != nil {
			slog.Warn("failed to save checkpoint", "phase", "users", "job", jobID, "error", err)
		}
	}
	if !completed["channels"] {
		s.extractChannels(ctx, sourceDB, job, stats, ids, channelOwners)
		if err := s.idMappingRepo.UpsertCheckpoint(ctx, jobID, "channels"); err != nil {
			slog.Warn("failed to save checkpoint", "phase", "channels", "job", jobID, "error", err)
		}
	}
	if !completed["videos"] {
		s.extractVideos(ctx, sourceDB, job, stats, ids, channelOwners)
		if err := s.idMappingRepo.UpsertCheckpoint(ctx, jobID, "videos"); err != nil {
			slog.Warn("failed to save checkpoint", "phase", "videos", "job", jobID, "error", err)
		}
	}
	if !completed["comments"] {
		s.extractComments(ctx, sourceDB, job, stats, ids)
		if err := s.idMappingRepo.UpsertCheckpoint(ctx, jobID, "comments"); err != nil {
			slog.Warn("failed to save checkpoint", "phase", "comments", "job", jobID, "error", err)
		}
	}
	if !completed["playlists"] {
		s.extractPlaylists(ctx, sourceDB, job, stats, ids)
		if err := s.idMappingRepo.UpsertCheckpoint(ctx, jobID, "playlists"); err != nil {
			slog.Warn("failed to save checkpoint", "phase", "playlists", "job", jobID, "error", err)
		}
	}
	if !completed["captions"] {
		s.extractCaptions(ctx, sourceDB, job, stats, ids)
		if err := s.idMappingRepo.UpsertCheckpoint(ctx, jobID, "captions"); err != nil {
			slog.Warn("failed to save checkpoint", "phase", "captions", "job", jobID, "error", err)
		}
	}

	s.extractMedia(ctx, job, stats)
	s.validateMigration(ctx, job, stats)

	completedAt := time.Now()
	job.CompletedAt = &completedAt
	job.Status = domain.MigrationStatusCompleted

	if err := job.SetStats(stats); err != nil {
		slog.Info(fmt.Sprintf("resume pipeline: failed to set stats for job %s: %v", jobID, err))
	}
	if err := s.repo.Update(ctx, job); err != nil {
		slog.Info(fmt.Sprintf("resume pipeline: failed to update job %s to completed: %v", jobID, err))
	}
}

// ---------------------------------------------------------------------------
// Pipeline orchestration
// ---------------------------------------------------------------------------

func (s *ETLService) runPipeline(jobID, adminUserID string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Info(fmt.Sprintf("migration pipeline: recovered panic in job %s: %v", jobID, r))
		}
	}()

	ctx := context.Background()

	job, err := s.repo.GetByID(ctx, jobID)
	if err != nil {
		slog.Info(fmt.Sprintf("migration pipeline: failed to load job %s: %v", jobID, err))
		return
	}

	if !job.CanTransition(domain.MigrationStatusRunning) {
		slog.Info(fmt.Sprintf("migration pipeline: job %s cannot transition to running from %s", jobID, job.Status))
		return
	}

	now := time.Now()
	job.Status = domain.MigrationStatusRunning
	job.StartedAt = &now
	if err := s.repo.Update(ctx, job); err != nil {
		slog.Info(fmt.Sprintf("migration pipeline: failed to update job %s to running: %v", jobID, err))
		return
	}

	sourceDB, err := connectSourceDB(job)
	if err != nil {
		s.failJob(ctx, job, fmt.Sprintf("source db connection failed: %v", err))
		return
	}
	defer sourceDB.Close()

	stats := &domain.MigrationStats{}
	ids := newIDMap()

	// channelOwners maps PT channel id → ownership info for video user_id resolution.
	channelOwners := make(map[int]*channelOwner)

	s.extractUsers(ctx, sourceDB, job, stats, ids)
	s.saveCheckpoint(ctx, jobID, "users")
	s.extractChannels(ctx, sourceDB, job, stats, ids, channelOwners)
	s.saveCheckpoint(ctx, jobID, "channels")
	s.extractVideos(ctx, sourceDB, job, stats, ids, channelOwners)
	s.saveCheckpoint(ctx, jobID, "videos")
	s.extractComments(ctx, sourceDB, job, stats, ids)
	s.saveCheckpoint(ctx, jobID, "comments")
	s.extractPlaylists(ctx, sourceDB, job, stats, ids)
	s.saveCheckpoint(ctx, jobID, "playlists")
	s.extractCaptions(ctx, sourceDB, job, stats, ids)
	s.saveCheckpoint(ctx, jobID, "captions")
	s.extractMedia(ctx, job, stats)
	s.validateMigration(ctx, job, stats)

	completedAt := time.Now()
	job.CompletedAt = &completedAt

	totalFailed := stats.Users.Failed + stats.Channels.Failed +
		stats.Videos.Failed + stats.Comments.Failed +
		stats.Playlists.Failed + stats.Captions.Failed + stats.Media.Failed

	if totalFailed > 0 {
		errMsg := fmt.Sprintf("migration completed with %d failures", totalFailed)
		job.ErrorMessage = &errMsg
	}

	job.Status = domain.MigrationStatusCompleted
	if err := job.SetStats(stats); err != nil {
		slog.Info(fmt.Sprintf("migration pipeline: failed to set stats for job %s: %v", jobID, err))
	}

	if err := s.repo.Update(ctx, job); err != nil {
		slog.Info(fmt.Sprintf("migration pipeline: failed to update job %s to completed: %v", jobID, err))
	}
}

func (s *ETLService) runDryRunPipeline(jobID string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Info(fmt.Sprintf("dry-run pipeline: recovered panic in job %s: %v", jobID, r))
		}
	}()

	ctx := context.Background()

	job, err := s.repo.GetByID(ctx, jobID)
	if err != nil {
		slog.Info(fmt.Sprintf("dry-run pipeline: failed to load job %s: %v", jobID, err))
		return
	}

	if !job.CanTransition(domain.MigrationStatusDryRun) {
		slog.Info(fmt.Sprintf("dry-run pipeline: job %s cannot transition to dry_run from %s", jobID, job.Status))
		return
	}

	now := time.Now()
	job.Status = domain.MigrationStatusDryRun
	job.StartedAt = &now
	if err := s.repo.Update(ctx, job); err != nil {
		slog.Info(fmt.Sprintf("dry-run pipeline: failed to update job %s to dry_run: %v", jobID, err))
		return
	}

	stats := &domain.MigrationStats{}
	s.dryRunExtract(ctx, job, stats)

	completedAt := time.Now()
	job.Status = domain.MigrationStatusCompleted
	job.CompletedAt = &completedAt

	if err := job.SetStats(stats); err != nil {
		slog.Info(fmt.Sprintf("dry-run pipeline: failed to set stats for job %s: %v", jobID, err))
	}

	if err := s.repo.Update(ctx, job); err != nil {
		slog.Info(fmt.Sprintf("dry-run pipeline: failed to update job %s to completed: %v", jobID, err))
	}
}

func (s *ETLService) failJob(ctx context.Context, job *domain.MigrationJob, errMsg string) {
	job.Status = domain.MigrationStatusFailed
	job.ErrorMessage = &errMsg
	now := time.Now()
	job.CompletedAt = &now
	if err := s.repo.Update(ctx, job); err != nil {
		slog.Info(fmt.Sprintf("migration pipeline: failed to mark job %s as failed: %v", job.ID, err))
	}
}

// ---------------------------------------------------------------------------
// ETL extraction phases
// ---------------------------------------------------------------------------

// ptUser represents a row from the PeerTube user+account join.
type ptUser struct {
	ID          int       `db:"id"`
	Username    string    `db:"username"`
	Email       string    `db:"email"`
	Role        int       `db:"role"`
	Blocked     bool      `db:"blocked"`
	AccountName string    `db:"account_name"`
	CreatedAt   time.Time `db:"created_at"`
}

func (s *ETLService) extractUsers(ctx context.Context, sourceDB *sqlx.DB, job *domain.MigrationJob, stats *domain.MigrationStats, ids *idMap) {
	slog.Info(fmt.Sprintf("migration %s: extracting users from %s", job.ID, job.SourceHost))

	const query = `
		SELECT u.id, u.username, u.email, u.role, u.blocked,
		       a.name AS account_name, u."createdAt" AS created_at
		FROM "user" u
		JOIN account a ON a."userId" = u.id`

	var rows []ptUser
	if err := sourceDB.SelectContext(ctx, &rows, query); err != nil {
		stats.Users.Errors = append(stats.Users.Errors, fmt.Sprintf("query users: %v", err))
		return
	}
	stats.Users.Total = len(rows)

	// Placeholder password hash for migrated users (they must reset password).
	const placeholderHash = "$2a$10$migrated000000000000000000000000000000000000000000"

	for _, r := range rows {
		now := time.Now().UTC()
		user := &domain.User{
			ID:          uuid.NewString(),
			Username:    r.Username,
			Email:       r.Email,
			DisplayName: r.AccountName,
			Role:        mapPeerTubeRole(r.Role),
			IsActive:    !r.Blocked,
			CreatedAt:   r.CreatedAt,
			UpdatedAt:   now,
		}

		if err := s.userRepo.Create(ctx, user, placeholderHash); err != nil {
			stats.Users.Failed++
			stats.Users.Errors = append(stats.Users.Errors, fmt.Sprintf("user %s: %v", r.Username, err))
			continue
		}

		ids.users[r.ID] = user.ID
		s.persistIDMapping(ctx, job.ID, "user", r.ID, user.ID)
		stats.Users.Migrated++
	}
}

// ptChannel represents a row from the PeerTube videoChannel + account join.
type ptChannel struct {
	ID          int            `db:"id"`
	Name        string         `db:"name"`
	Description sql.NullString `db:"description"`
	Support     sql.NullString `db:"support"`
	AccountID   int            `db:"account_id"`
	AccountName string         `db:"account_name"`
	CreatedAt   time.Time      `db:"created_at"`
}

func (s *ETLService) extractChannels(ctx context.Context, sourceDB *sqlx.DB, job *domain.MigrationJob, stats *domain.MigrationStats, ids *idMap, channelOwners map[int]*channelOwner) {
	slog.Info(fmt.Sprintf("migration %s: extracting channels from %s", job.ID, job.SourceHost))

	const query = `
		SELECT vc.id, vc.name, vc.description, vc.support,
		       vc."accountId" AS account_id, a.name AS account_name,
		       vc."createdAt" AS created_at
		FROM "videoChannel" vc
		JOIN account a ON a.id = vc."accountId"`

	var rows []ptChannel
	if err := sourceDB.SelectContext(ctx, &rows, query); err != nil {
		stats.Channels.Errors = append(stats.Channels.Errors, fmt.Sprintf("query channels: %v", err))
		return
	}
	stats.Channels.Total = len(rows)

	// Resolve account → user mapping. PeerTube account.userId links to user.id.
	type accountUser struct {
		AccountID int `db:"id"`
		UserID    int `db:"user_id"`
	}
	var acctUsers []accountUser
	if err := sourceDB.SelectContext(ctx, &acctUsers, `SELECT id, "userId" AS user_id FROM account WHERE "userId" IS NOT NULL`); err != nil {
		stats.Channels.Errors = append(stats.Channels.Errors, fmt.Sprintf("query account→user map: %v", err))
		return
	}
	acctToUser := make(map[int]int, len(acctUsers))
	for _, au := range acctUsers {
		acctToUser[au.AccountID] = au.UserID
	}

	for _, r := range rows {
		// Resolve owner user ID
		ptUserID, ok := acctToUser[r.AccountID]
		if !ok {
			stats.Channels.Failed++
			stats.Channels.Errors = append(stats.Channels.Errors, fmt.Sprintf("channel %s: no user for account %d", r.Name, r.AccountID))
			continue
		}
		ownerID, ok := ids.users[ptUserID]
		if !ok {
			stats.Channels.Failed++
			stats.Channels.Errors = append(stats.Channels.Errors, fmt.Sprintf("channel %s: user %d not migrated", r.Name, ptUserID))
			continue
		}

		ownerUUID, _ := uuid.Parse(ownerID)
		chID := uuid.New()
		now := time.Now().UTC()

		var desc *string
		if r.Description.Valid {
			desc = &r.Description.String
		}
		var support *string
		if r.Support.Valid {
			support = &r.Support.String
		}

		ch := &domain.Channel{
			ID:          chID,
			AccountID:   ownerUUID,
			UserID:      ownerUUID,
			Handle:      r.Name,
			Name:        r.Name,
			DisplayName: r.Name,
			Description: desc,
			Support:     support,
			IsLocal:     true,
			CreatedAt:   r.CreatedAt,
			UpdatedAt:   now,
		}

		if err := s.channelRepo.Create(ctx, ch); err != nil {
			stats.Channels.Failed++
			stats.Channels.Errors = append(stats.Channels.Errors, fmt.Sprintf("channel %s: %v", r.Name, err))
			continue
		}

		ids.channels[r.ID] = chID
		s.persistIDMapping(ctx, job.ID, "channel", r.ID, chID.String())
		channelOwners[r.ID] = &channelOwner{userID: ownerID, channelID: chID}
		stats.Channels.Migrated++
	}
}

// ptVideo represents a row from the PeerTube video table.
type ptVideo struct {
	ID          int       `db:"id"`
	UUID        string    `db:"uuid"`
	Name        string    `db:"name"`
	Description string    `db:"description"`
	Privacy     int       `db:"privacy"`
	Duration    int       `db:"duration"`
	Views       int64     `db:"views"`
	Language    string    `db:"language"`
	ChannelID   int       `db:"channel_id"`
	PublishedAt time.Time `db:"published_at"`
	CreatedAt   time.Time `db:"created_at"`
}

func (s *ETLService) extractVideos(ctx context.Context, sourceDB *sqlx.DB, job *domain.MigrationJob, stats *domain.MigrationStats, ids *idMap, channelOwners map[int]*channelOwner) {
	slog.Info(fmt.Sprintf("migration %s: extracting videos from %s", job.ID, job.SourceHost))

	const query = `
		SELECT v.id, v.uuid, v.name, COALESCE(v.description, '') AS description,
		       v.privacy, v.duration, v.views,
		       COALESCE(v.language, '') AS language,
		       v."channelId" AS channel_id,
		       v."publishedAt" AS published_at, v."createdAt" AS created_at
		FROM video v
		WHERE v.remote = false`

	var rows []ptVideo
	if err := sourceDB.SelectContext(ctx, &rows, query); err != nil {
		stats.Videos.Errors = append(stats.Videos.Errors, fmt.Sprintf("query videos: %v", err))
		return
	}
	stats.Videos.Total = len(rows)

	// Build all valid video objects first (no DB calls).
	type videoWithSource struct {
		video    *domain.Video
		sourceID int
		name     string
	}
	var validVideos []videoWithSource
	for _, r := range rows {
		owner, ok := channelOwners[r.ChannelID]
		if !ok {
			stats.Videos.Failed++
			stats.Videos.Errors = append(stats.Videos.Errors, fmt.Sprintf("video %s: channel %d not migrated", r.Name, r.ChannelID))
			continue
		}

		now := time.Now().UTC()
		video := &domain.Video{
			ID:          uuid.NewString(),
			Title:       r.Name,
			Description: r.Description,
			Privacy:     mapPeerTubePrivacy(r.Privacy),
			Status:      domain.StatusCompleted,
			Duration:    r.Duration,
			Views:       r.Views,
			Language:    r.Language,
			UserID:      owner.userID,
			ChannelID:   owner.channelID,
			UploadDate:  r.PublishedAt,
			CreatedAt:   r.CreatedAt,
			UpdatedAt:   now,
		}
		validVideos = append(validVideos, videoWithSource{video: video, sourceID: r.ID, name: r.Name})
	}

	// Batch insert with fallback to individual creates.
	if batcher, ok := s.videoRepo.(port.VideoBatchCreator); ok && len(validVideos) > 0 {
		batch := make([]*domain.Video, len(validVideos))
		for i, vws := range validVideos {
			batch[i] = vws.video
		}
		if batchErr := batcher.CreateBatch(ctx, batch); batchErr == nil {
			for _, vws := range validVideos {
				ids.videos[vws.sourceID] = vws.video.ID
				s.persistIDMapping(ctx, job.ID, "video", vws.sourceID, vws.video.ID)
				stats.Videos.Migrated++
			}
			return
		} else {
			slog.Info(fmt.Sprintf("migration %s: batch video insert failed, falling back to individual inserts: %v", job.ID, batchErr))
		}
	}

	// Fallback: individual inserts with per-item error tracking.
	for _, vws := range validVideos {
		if err := s.videoRepo.Create(ctx, vws.video); err != nil {
			stats.Videos.Failed++
			stats.Videos.Errors = append(stats.Videos.Errors, fmt.Sprintf("video %s: %v", vws.name, err))
			continue
		}
		ids.videos[vws.sourceID] = vws.video.ID
		s.persistIDMapping(ctx, job.ID, "video", vws.sourceID, vws.video.ID)
		stats.Videos.Migrated++
	}
}

// ptComment represents a row from PeerTube's videoComment table.
type ptComment struct {
	ID            int           `db:"id"`
	Text          string        `db:"text"`
	VideoID       int           `db:"video_id"`
	AccountID     int           `db:"account_id"`
	InReplyToID   sql.NullInt64 `db:"in_reply_to_id"`
	CreatedAt     time.Time     `db:"created_at"`
	AccountUserID sql.NullInt64 `db:"account_user_id"`
}

func (s *ETLService) extractComments(ctx context.Context, sourceDB *sqlx.DB, job *domain.MigrationJob, stats *domain.MigrationStats, ids *idMap) {
	slog.Info(fmt.Sprintf("migration %s: extracting comments from %s", job.ID, job.SourceHost))

	const query = `
		SELECT vc.id, vc.text,
		       vc."videoId" AS video_id, vc."accountId" AS account_id,
		       vc."inReplyToCommentId" AS in_reply_to_id,
		       vc."createdAt" AS created_at,
		       a."userId" AS account_user_id
		FROM "videoComment" vc
		JOIN account a ON a.id = vc."accountId"
		WHERE vc."deletedAt" IS NULL
		ORDER BY vc.id`

	var rows []ptComment
	if err := sourceDB.SelectContext(ctx, &rows, query); err != nil {
		stats.Comments.Errors = append(stats.Comments.Errors, fmt.Sprintf("query comments: %v", err))
		return
	}
	stats.Comments.Total = len(rows)

	// Two-pass: top-level first, then replies (so parent UUIDs are available).
	topLevel := make([]ptComment, 0, len(rows))
	replies := make([]ptComment, 0)
	for _, r := range rows {
		if !r.InReplyToID.Valid {
			topLevel = append(topLevel, r)
		} else {
			replies = append(replies, r)
		}
	}

	insertComment := func(r ptComment, parentID *uuid.UUID) {
		videoID, ok := ids.videos[r.VideoID]
		if !ok {
			stats.Comments.Failed++
			stats.Comments.Errors = append(stats.Comments.Errors, fmt.Sprintf("comment %d: video %d not migrated", r.ID, r.VideoID))
			return
		}

		var userID string
		if r.AccountUserID.Valid {
			uid, ok := ids.users[int(r.AccountUserID.Int64)]
			if !ok {
				stats.Comments.Failed++
				stats.Comments.Errors = append(stats.Comments.Errors, fmt.Sprintf("comment %d: user for account %d not migrated", r.ID, r.AccountID))
				return
			}
			userID = uid
		} else {
			stats.Comments.Failed++
			stats.Comments.Errors = append(stats.Comments.Errors, fmt.Sprintf("comment %d: account %d has no user", r.ID, r.AccountID))
			return
		}

		videoUUID, _ := uuid.Parse(videoID)
		userUUID, _ := uuid.Parse(userID)
		now := time.Now().UTC()

		comment := &domain.Comment{
			ID:        uuid.New(),
			VideoID:   videoUUID,
			UserID:    userUUID,
			ParentID:  parentID,
			Body:      r.Text,
			Status:    domain.CommentStatusActive,
			CreatedAt: r.CreatedAt,
			UpdatedAt: now,
		}

		if err := s.commentRepo.Create(ctx, comment); err != nil {
			stats.Comments.Failed++
			stats.Comments.Errors = append(stats.Comments.Errors, fmt.Sprintf("comment %d: %v", r.ID, err))
			return
		}

		ids.comments[r.ID] = comment.ID
		s.persistIDMapping(ctx, job.ID, "comment", r.ID, comment.ID.String())
		stats.Comments.Migrated++
	}

	for _, r := range topLevel {
		insertComment(r, nil)
	}
	for _, r := range replies {
		parentVidraID, ok := ids.comments[int(r.InReplyToID.Int64)]
		if !ok {
			stats.Comments.Failed++
			stats.Comments.Errors = append(stats.Comments.Errors, fmt.Sprintf("comment %d: parent %d not migrated", r.ID, r.InReplyToID.Int64))
			continue
		}
		insertComment(r, &parentVidraID)
	}
}

// ptPlaylist represents a row from PeerTube's videoPlaylist table.
type ptPlaylist struct {
	ID             int            `db:"id"`
	Name           string         `db:"name"`
	Description    sql.NullString `db:"description"`
	Privacy        int            `db:"privacy"`
	OwnerAccountID int            `db:"owner_account_id"`
	CreatedAt      time.Time      `db:"created_at"`
}

// ptPlaylistElement represents a row from PeerTube's videoPlaylistElement table.
type ptPlaylistElement struct {
	PlaylistID int `db:"playlist_id"`
	VideoID    int `db:"video_id"`
	Position   int `db:"position"`
}

func (s *ETLService) extractPlaylists(ctx context.Context, sourceDB *sqlx.DB, job *domain.MigrationJob, stats *domain.MigrationStats, ids *idMap) {
	slog.Info(fmt.Sprintf("migration %s: extracting playlists from %s", job.ID, job.SourceHost))

	// Resolve account → user map for playlist ownership.
	type accountUser struct {
		AccountID int `db:"id"`
		UserID    int `db:"user_id"`
	}
	var acctUsers []accountUser
	if err := sourceDB.SelectContext(ctx, &acctUsers, `SELECT id, "userId" AS user_id FROM account WHERE "userId" IS NOT NULL`); err != nil {
		stats.Playlists.Errors = append(stats.Playlists.Errors, fmt.Sprintf("query account→user: %v", err))
		return
	}
	acctToUser := make(map[int]int, len(acctUsers))
	for _, au := range acctUsers {
		acctToUser[au.AccountID] = au.UserID
	}

	const playlistQuery = `
		SELECT vp.id, vp.name, vp.description, vp.privacy,
		       vp."ownerAccountId" AS owner_account_id,
		       vp."createdAt" AS created_at
		FROM "videoPlaylist" vp`

	var playlists []ptPlaylist
	if err := sourceDB.SelectContext(ctx, &playlists, playlistQuery); err != nil {
		stats.Playlists.Errors = append(stats.Playlists.Errors, fmt.Sprintf("query playlists: %v", err))
		return
	}
	stats.Playlists.Total = len(playlists)

	const elemQuery = `
		SELECT "videoPlaylistId" AS playlist_id, "videoId" AS video_id, position
		FROM "videoPlaylistElement"
		ORDER BY "videoPlaylistId", position`

	var elements []ptPlaylistElement
	if err := sourceDB.SelectContext(ctx, &elements, elemQuery); err != nil {
		stats.Playlists.Errors = append(stats.Playlists.Errors, fmt.Sprintf("query playlist elements: %v", err))
		return
	}

	elemsByPlaylist := make(map[int][]ptPlaylistElement)
	for _, e := range elements {
		elemsByPlaylist[e.PlaylistID] = append(elemsByPlaylist[e.PlaylistID], e)
	}

	for _, p := range playlists {
		ptUserID, ok := acctToUser[p.OwnerAccountID]
		if !ok {
			stats.Playlists.Failed++
			stats.Playlists.Errors = append(stats.Playlists.Errors, fmt.Sprintf("playlist %s: no user for account %d", p.Name, p.OwnerAccountID))
			continue
		}
		ownerID, ok := ids.users[ptUserID]
		if !ok {
			stats.Playlists.Failed++
			stats.Playlists.Errors = append(stats.Playlists.Errors, fmt.Sprintf("playlist %s: user %d not migrated", p.Name, ptUserID))
			continue
		}

		ownerUUID, _ := uuid.Parse(ownerID)
		now := time.Now().UTC()

		var desc *string
		if p.Description.Valid {
			desc = &p.Description.String
		}

		playlist := &domain.Playlist{
			ID:          uuid.New(),
			UserID:      ownerUUID,
			Name:        p.Name,
			Description: desc,
			Privacy:     mapPeerTubePrivacy(p.Privacy),
			CreatedAt:   p.CreatedAt,
			UpdatedAt:   now,
		}

		if err := s.playlistRepo.Create(ctx, playlist); err != nil {
			stats.Playlists.Failed++
			stats.Playlists.Errors = append(stats.Playlists.Errors, fmt.Sprintf("playlist %s: %v", p.Name, err))
			continue
		}

		// Add items
		for _, elem := range elemsByPlaylist[p.ID] {
			videoID, ok := ids.videos[elem.VideoID]
			if !ok {
				slog.Info(fmt.Sprintf("migration %s: playlist item skipped — video %d not migrated", job.ID, elem.VideoID))
				continue
			}
			videoUUID, _ := uuid.Parse(videoID)
			pos := elem.Position
			if err := s.playlistRepo.AddItem(ctx, playlist.ID, videoUUID, &pos); err != nil {
				slog.Info(fmt.Sprintf("migration %s: failed to add item to playlist %s: %v", job.ID, playlist.ID, err))
			}
		}

		s.persistIDMapping(ctx, job.ID, "playlist", p.ID, playlist.ID.String())
		stats.Playlists.Migrated++
	}
}

// ptCaption represents a row from PeerTube's videoCaption table.
type ptCaption struct {
	ID       int    `db:"id"`
	VideoID  int    `db:"video_id"`
	Language string `db:"language"`
	Filename string `db:"filename"`
}

func (s *ETLService) extractCaptions(ctx context.Context, sourceDB *sqlx.DB, job *domain.MigrationJob, stats *domain.MigrationStats, ids *idMap) {
	slog.Info(fmt.Sprintf("migration %s: extracting captions from %s", job.ID, job.SourceHost))

	const query = `
		SELECT id, "videoId" AS video_id, language, COALESCE(filename, '') AS filename
		FROM "videoCaption"`

	var rows []ptCaption
	if err := sourceDB.SelectContext(ctx, &rows, query); err != nil {
		stats.Captions.Errors = append(stats.Captions.Errors, fmt.Sprintf("query captions: %v", err))
		return
	}
	stats.Captions.Total = len(rows)

	for _, r := range rows {
		videoID, ok := ids.videos[r.VideoID]
		if !ok {
			stats.Captions.Failed++
			stats.Captions.Errors = append(stats.Captions.Errors, fmt.Sprintf("caption %d: video %d not migrated", r.ID, r.VideoID))
			continue
		}
		videoUUID, _ := uuid.Parse(videoID)
		now := time.Now().UTC()

		caption := &domain.Caption{
			ID:           uuid.New(),
			VideoID:      videoUUID,
			LanguageCode: r.Language,
			Label:        r.Language,
			FileFormat:   domain.CaptionFormatVTT,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		if err := s.captionRepo.Create(ctx, caption); err != nil {
			stats.Captions.Failed++
			stats.Captions.Errors = append(stats.Captions.Errors, fmt.Sprintf("caption %d: %v", r.ID, err))
			continue
		}
		s.persistIDMapping(ctx, job.ID, "caption", r.ID, caption.ID.String())
		stats.Captions.Migrated++
	}
}

// persistIDMapping writes a PeerTube→Vidra ID mapping to the database (best-effort).
func (s *ETLService) persistIDMapping(ctx context.Context, jobID, entityType string, peertubeID int, vidraID string) {
	if s.idMappingRepo == nil {
		return
	}
	mapping := &domain.MigrationIDMapping{
		JobID:      jobID,
		EntityType: entityType,
		PeertubeID: peertubeID,
		VidraID:    vidraID,
	}
	if err := s.idMappingRepo.Upsert(ctx, mapping); err != nil {
		slog.Info(fmt.Sprintf("migration: failed to persist id mapping %s/%d→%s: %v", entityType, peertubeID, vidraID, err))
	}
}

// saveCheckpoint records that an ETL phase completed (best-effort).
func (s *ETLService) saveCheckpoint(ctx context.Context, jobID, entityType string) {
	if s.idMappingRepo == nil {
		return
	}
	if err := s.idMappingRepo.UpsertCheckpoint(ctx, jobID, entityType); err != nil {
		slog.Info(fmt.Sprintf("migration: failed to save checkpoint %s/%s: %v", jobID, entityType, err))
	}
}

func (s *ETLService) extractMedia(_ context.Context, job *domain.MigrationJob, stats *domain.MigrationStats) {
	slog.Info(fmt.Sprintf("migration %s: media transfer skipped (requires filesystem access)", job.ID))
	stats.Media.Skipped = stats.Videos.Migrated
}

func (s *ETLService) validateMigration(_ context.Context, job *domain.MigrationJob, stats *domain.MigrationStats) {
	slog.Info(fmt.Sprintf("migration %s: validating — users=%d/%d channels=%d/%d videos=%d/%d comments=%d/%d playlists=%d/%d captions=%d/%d",
		job.ID,
		stats.Users.Migrated, stats.Users.Total,
		stats.Channels.Migrated, stats.Channels.Total,
		stats.Videos.Migrated, stats.Videos.Total,
		stats.Comments.Migrated, stats.Comments.Total,
		stats.Playlists.Migrated, stats.Playlists.Total,
		stats.Captions.Migrated, stats.Captions.Total,
	))
}

func (s *ETLService) dryRunExtract(ctx context.Context, job *domain.MigrationJob, stats *domain.MigrationStats) {
	slog.Info(fmt.Sprintf("migration %s: dry-run counting source entities from %s", job.ID, job.SourceHost))

	sourceDB, err := connectSourceDB(job)
	if err != nil {
		stats.Users.Errors = append(stats.Users.Errors, fmt.Sprintf("source db: %v", err))
		return
	}
	defer sourceDB.Close()

	type countResult struct {
		Count int `db:"count"`
	}

	tables := []struct {
		query string
		stat  *domain.EntityStats
	}{
		{`SELECT COUNT(*) AS count FROM "user"`, &stats.Users},
		{`SELECT COUNT(*) AS count FROM "videoChannel"`, &stats.Channels},
		{`SELECT COUNT(*) AS count FROM video WHERE remote = false`, &stats.Videos},
		{`SELECT COUNT(*) AS count FROM "videoComment" WHERE "deletedAt" IS NULL`, &stats.Comments},
		{`SELECT COUNT(*) AS count FROM "videoPlaylist"`, &stats.Playlists},
		{`SELECT COUNT(*) AS count FROM "videoCaption"`, &stats.Captions},
	}

	for _, t := range tables {
		var result countResult
		if err := sourceDB.GetContext(ctx, &result, t.query); err != nil {
			t.stat.Errors = append(t.stat.Errors, fmt.Sprintf("count: %v", err))
			continue
		}
		t.stat.Total = result.Count
	}
}
