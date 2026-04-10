package domain

import (
	"encoding/json"
	"errors"
	"time"
)

// MigrationStatus represents the status of a migration job
type MigrationStatus string

const (
	MigrationStatusPending    MigrationStatus = "pending"
	MigrationStatusRunning    MigrationStatus = "running"
	MigrationStatusCompleted  MigrationStatus = "completed"
	MigrationStatusFailed     MigrationStatus = "failed"
	MigrationStatusCancelled  MigrationStatus = "cancelled"
	MigrationStatusDryRun     MigrationStatus = "dry_run"
	MigrationStatusValidating MigrationStatus = "validating"
	MigrationStatusResuming   MigrationStatus = "resuming"
)

// IsTerminal returns true if the migration status is in a terminal state.
// Note: "failed" is NOT terminal because it can transition to "resuming" for resume support.
func (s MigrationStatus) IsTerminal() bool {
	return s == MigrationStatusCompleted || s == MigrationStatusCancelled
}

// MigrationJob represents an instance migration job from a PeerTube instance
type MigrationJob struct {
	ID               string          `db:"id" json:"id"`
	AdminUserID      string          `db:"admin_user_id" json:"admin_user_id"`
	SourceHost       string          `db:"source_host" json:"source_host"`
	Status           MigrationStatus `db:"status" json:"status"`
	DryRun           bool            `db:"dry_run" json:"dry_run"`
	ErrorMessage     *string         `db:"error_message" json:"error_message,omitempty"`
	StatsJSON        json.RawMessage `db:"stats_json" json:"stats,omitempty"`
	SourceDBHost     *string         `db:"source_db_host" json:"source_db_host,omitempty"`
	SourceDBPort     *int            `db:"source_db_port" json:"source_db_port,omitempty"`
	SourceDBName     *string         `db:"source_db_name" json:"source_db_name,omitempty"`
	SourceDBUser     *string         `db:"source_db_user" json:"source_db_user,omitempty"`
	SourceDBPassword *string         `db:"source_db_password" json:"-"` // Never expose password
	SourceMediaPath  *string         `db:"source_media_path" json:"source_media_path,omitempty"`
	CreatedAt        time.Time       `db:"created_at" json:"created_at"`
	StartedAt        *time.Time      `db:"started_at" json:"started_at,omitempty"`
	CompletedAt      *time.Time      `db:"completed_at" json:"completed_at,omitempty"`
	UpdatedAt        time.Time       `db:"updated_at" json:"updated_at"`
}

// MigrationStats tracks progress across each ETL entity type
type MigrationStats struct {
	Users     EntityStats `json:"users"`
	Channels  EntityStats `json:"channels"`
	Videos    EntityStats `json:"videos"`
	Comments  EntityStats `json:"comments"`
	Playlists EntityStats `json:"playlists"`
	Captions  EntityStats `json:"captions"`
	Media     EntityStats `json:"media"`
}

// EntityStats tracks individual entity migration metrics
type EntityStats struct {
	Total    int      `json:"total"`
	Migrated int      `json:"migrated"`
	Skipped  int      `json:"skipped"`
	Failed   int      `json:"failed"`
	Errors   []string `json:"errors,omitempty"`
}

// MigrationRequest is the input for starting a new migration
type MigrationRequest struct {
	SourceHost       string `json:"source_host" validate:"required"`
	SourceDBHost     string `json:"source_db_host" validate:"required"`
	SourceDBPort     int    `json:"source_db_port"`
	SourceDBName     string `json:"source_db_name" validate:"required"`
	SourceDBUser     string `json:"source_db_user" validate:"required"`
	SourceDBPassword string `json:"source_db_password" validate:"required"`
	SourceMediaPath  string `json:"source_media_path"`
}

// Validate validates the migration request fields
func (r *MigrationRequest) Validate() error {
	if r.SourceHost == "" {
		return errors.New("source_host is required")
	}
	if r.SourceDBHost == "" {
		return errors.New("source_db_host is required")
	}
	if r.SourceDBName == "" {
		return errors.New("source_db_name is required")
	}
	if r.SourceDBUser == "" {
		return errors.New("source_db_user is required")
	}
	if r.SourceDBPassword == "" {
		return errors.New("source_db_password is required")
	}
	if r.SourceDBPort <= 0 {
		r.SourceDBPort = 5432
	}
	return nil
}

// GetStats parses and returns the migration stats from JSON
func (j *MigrationJob) GetStats() (*MigrationStats, error) {
	if len(j.StatsJSON) == 0 {
		return &MigrationStats{}, nil
	}

	var stats MigrationStats
	if err := json.Unmarshal(j.StatsJSON, &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

// SetStats serializes the stats and stores as JSON
func (j *MigrationJob) SetStats(stats *MigrationStats) error {
	data, err := json.Marshal(stats)
	if err != nil {
		return err
	}
	j.StatsJSON = data
	return nil
}

// CanTransition checks if a status transition is valid
func (j *MigrationJob) CanTransition(newStatus MigrationStatus) bool {
	if j.Status.IsTerminal() {
		return false
	}

	validTransitions := map[MigrationStatus][]MigrationStatus{
		MigrationStatusPending: {
			MigrationStatusRunning,
			MigrationStatusCancelled,
			MigrationStatusFailed,
			MigrationStatusDryRun,
			MigrationStatusValidating,
		},
		MigrationStatusValidating: {
			MigrationStatusRunning,
			MigrationStatusFailed,
			MigrationStatusCancelled,
		},
		MigrationStatusDryRun: {
			MigrationStatusCompleted,
			MigrationStatusFailed,
		},
		MigrationStatusRunning: {
			MigrationStatusCompleted,
			MigrationStatusFailed,
			MigrationStatusCancelled,
		},
		MigrationStatusFailed: {
			MigrationStatusResuming,
		},
		MigrationStatusResuming: {
			MigrationStatusRunning,
			MigrationStatusFailed,
		},
	}

	allowed, exists := validTransitions[j.Status]
	if !exists {
		return false
	}

	for _, status := range allowed {
		if status == newStatus {
			return true
		}
	}

	return false
}

// MigrationIDMapping represents a PeerTube integer ID → Vidra Core ID mapping
type MigrationIDMapping struct {
	JobID      string    `db:"job_id" json:"job_id"`
	EntityType string    `db:"entity_type" json:"entity_type"`
	PeertubeID int       `db:"peertube_id" json:"peertube_id"`
	VidraID    string    `db:"vidra_id" json:"vidra_id"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
}

// MigrationCheckpoint records completion of an ETL phase for resume support
type MigrationCheckpoint struct {
	JobID       string    `db:"job_id" json:"job_id"`
	EntityType  string    `db:"entity_type" json:"entity_type"`
	CompletedAt time.Time `db:"completed_at" json:"completed_at"`
}

// Migration sentinel errors
var (
	ErrMigrationNotFound     = errors.New("migration job not found")
	ErrMigrationInProgress   = errors.New("a migration is already in progress")
	ErrMigrationCantCancel   = errors.New("migration cannot be cancelled in current state")
	ErrMigrationCantResume   = errors.New("migration cannot resume from current state")
	ErrMigrationSourceFailed = errors.New("failed to connect to source database")
	ErrMigrationValidation   = errors.New("migration validation failed")
)
