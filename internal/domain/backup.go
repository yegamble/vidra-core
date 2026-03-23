package domain

import "time"

type BackupStatus string

const (
	BackupStatusPending   BackupStatus = "pending"
	BackupStatusRunning   BackupStatus = "running"
	BackupStatusCompleted BackupStatus = "completed"
	BackupStatusFailed    BackupStatus = "failed"
)

type BackupJob struct {
	ID            string       `json:"id"`
	Status        BackupStatus `json:"status"`
	CreatedAt     time.Time    `json:"created_at"`
	StartedAt     *time.Time   `json:"started_at,omitempty"`
	CompletedAt   *time.Time   `json:"completed_at,omitempty"`
	SchemaVersion int64        `json:"schema_version"`
	Error         string       `json:"error,omitempty"`
}

type BackupResult struct {
	JobID         string    `json:"job_id"`
	Success       bool      `json:"success"`
	BytesSize     int64     `json:"bytes_size"`
	Message       string    `json:"message"`
	BackupPath    string    `json:"backup_path,omitempty"`
	SchemaVersion int64     `json:"schema_version"`
	CompletedAt   time.Time `json:"completed_at"`
	Error         string    `json:"error,omitempty"`
}
