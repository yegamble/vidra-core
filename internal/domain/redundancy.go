package domain

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

// RedundancyStatus represents the status of a video redundancy operation
type RedundancyStatus string

const (
	RedundancyStatusPending   RedundancyStatus = "pending"
	RedundancyStatusSyncing   RedundancyStatus = "syncing"
	RedundancyStatusSynced    RedundancyStatus = "synced"
	RedundancyStatusFailed    RedundancyStatus = "failed"
	RedundancyStatusCancelled RedundancyStatus = "cancelled"
)

// RedundancyStrategy represents the strategy for selecting videos for redundancy
type RedundancyStrategy string

const (
	RedundancyStrategyRecent     RedundancyStrategy = "recent"
	RedundancyStrategyMostViewed RedundancyStrategy = "most_viewed"
	RedundancyStrategyTrending   RedundancyStrategy = "trending"
	RedundancyStrategyManual     RedundancyStrategy = "manual"
	RedundancyStrategyAll        RedundancyStrategy = "all"
)

// InstancePeer represents a known peer instance for redundancy
type InstancePeer struct {
	ID           string `json:"id" db:"id"`
	InstanceURL  string `json:"instance_url" db:"instance_url"`
	InstanceName string `json:"instance_name" db:"instance_name"`
	InstanceHost string `json:"instance_host" db:"instance_host"`
	Software     string `json:"software" db:"software"`
	Version      string `json:"version" db:"version"`

	// Redundancy configuration
	AutoAcceptRedundancy bool `json:"auto_accept_redundancy" db:"auto_accept_redundancy"`
	MaxRedundancySizeGB  int  `json:"max_redundancy_size_gb" db:"max_redundancy_size_gb"`
	AcceptsNewRedundancy bool `json:"accepts_new_redundancy" db:"accepts_new_redundancy"`

	// Health metrics
	LastContactedAt   *time.Time `json:"last_contacted_at,omitempty" db:"last_contacted_at"`
	LastSyncSuccessAt *time.Time `json:"last_sync_success_at,omitempty" db:"last_sync_success_at"`
	LastSyncError     string     `json:"last_sync_error,omitempty" db:"last_sync_error"`
	FailedSyncCount   int        `json:"failed_sync_count" db:"failed_sync_count"`
	IsActive          bool       `json:"is_active" db:"is_active"`

	// ActivityPub actor information
	ActorURL       string `json:"actor_url,omitempty" db:"actor_url"`
	InboxURL       string `json:"inbox_url,omitempty" db:"inbox_url"`
	SharedInboxURL string `json:"shared_inbox_url,omitempty" db:"shared_inbox_url"`
	PublicKey      string `json:"public_key,omitempty" db:"public_key"`

	// Statistics
	TotalVideosStored int   `json:"total_videos_stored" db:"total_videos_stored"`
	TotalStorageBytes int64 `json:"total_storage_bytes" db:"total_storage_bytes"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// VideoRedundancy represents a redundancy copy of a video
type VideoRedundancy struct {
	ID               string `json:"id" db:"id"`
	VideoID          string `json:"video_id" db:"video_id"`
	TargetInstanceID string `json:"target_instance_id" db:"target_instance_id"`

	// URLs and identifiers
	TargetVideoURL string `json:"target_video_url,omitempty" db:"target_video_url"`
	TargetVideoID  string `json:"target_video_id,omitempty" db:"target_video_id"`

	// Status and strategy
	Status   RedundancyStatus   `json:"status" db:"status"`
	Strategy RedundancyStrategy `json:"strategy" db:"strategy"`

	// File information
	FileSizeBytes      int64      `json:"file_size_bytes" db:"file_size_bytes"`
	ChecksumSHA256     string     `json:"checksum_sha256,omitempty" db:"checksum_sha256"`
	ChecksumVerifiedAt *time.Time `json:"checksum_verified_at,omitempty" db:"checksum_verified_at"`

	// Sync progress
	BytesTransferred      int64      `json:"bytes_transferred" db:"bytes_transferred"`
	TransferSpeedBPS      int64      `json:"transfer_speed_bps" db:"transfer_speed_bps"`
	EstimatedCompletionAt *time.Time `json:"estimated_completion_at,omitempty" db:"estimated_completion_at"`

	// Sync status
	SyncStartedAt    *time.Time `json:"sync_started_at,omitempty" db:"sync_started_at"`
	LastSyncAt       *time.Time `json:"last_sync_at,omitempty" db:"last_sync_at"`
	NextSyncAt       *time.Time `json:"next_sync_at,omitempty" db:"next_sync_at"`
	SyncAttemptCount int        `json:"sync_attempt_count" db:"sync_attempt_count"`
	MaxSyncAttempts  int        `json:"max_sync_attempts" db:"max_sync_attempts"`
	SyncError        string     `json:"sync_error,omitempty" db:"sync_error"`

	// Priority and scheduling
	Priority   int  `json:"priority" db:"priority"`
	AutoResync bool `json:"auto_resync" db:"auto_resync"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// RedundancyPolicy defines automatic redundancy policies
type RedundancyPolicy struct {
	ID          string `json:"id" db:"id"`
	Name        string `json:"name" db:"name"`
	Description string `json:"description" db:"description"`

	// Policy configuration
	Strategy RedundancyStrategy `json:"strategy" db:"strategy"`
	Enabled  bool               `json:"enabled" db:"enabled"`

	// Selection criteria
	MinViews     int      `json:"min_views" db:"min_views"`
	MinAgeDays   int      `json:"min_age_days" db:"min_age_days"`
	MaxAgeDays   *int     `json:"max_age_days,omitempty" db:"max_age_days"`
	PrivacyTypes []string `json:"privacy_types" db:"privacy_types"`

	// Redundancy targets
	TargetInstanceCount int `json:"target_instance_count" db:"target_instance_count"`
	MinInstanceCount    int `json:"min_instance_count" db:"min_instance_count"`

	// Size limits
	MaxVideoSizeGB *int `json:"max_video_size_gb,omitempty" db:"max_video_size_gb"`
	MaxTotalSizeGB *int `json:"max_total_size_gb,omitempty" db:"max_total_size_gb"`

	// Scheduling
	EvaluationIntervalHours int        `json:"evaluation_interval_hours" db:"evaluation_interval_hours"`
	LastEvaluatedAt         *time.Time `json:"last_evaluated_at,omitempty" db:"last_evaluated_at"`
	NextEvaluationAt        *time.Time `json:"next_evaluation_at,omitempty" db:"next_evaluation_at"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// RedundancySyncLog records detailed sync operation logs
type RedundancySyncLog struct {
	ID           string `json:"id" db:"id"`
	RedundancyID string `json:"redundancy_id" db:"redundancy_id"`

	// Sync attempt information
	AttemptNumber int        `json:"attempt_number" db:"attempt_number"`
	StartedAt     time.Time  `json:"started_at" db:"started_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty" db:"completed_at"`

	// Transfer metrics
	BytesTransferred    int64  `json:"bytes_transferred" db:"bytes_transferred"`
	TransferDurationSec *int   `json:"transfer_duration_seconds,omitempty" db:"transfer_duration_seconds"`
	AverageSpeedBPS     *int64 `json:"average_speed_bps,omitempty" db:"average_speed_bps"`

	// Result
	Success      bool   `json:"success" db:"success"`
	ErrorMessage string `json:"error_message,omitempty" db:"error_message"`
	ErrorType    string `json:"error_type,omitempty" db:"error_type"` // network, auth, storage, checksum, timeout

	// Additional context
	HTTPStatusCode *int `json:"http_status_code,omitempty" db:"http_status_code"`
	RetryAfterSec  *int `json:"retry_after_seconds,omitempty" db:"retry_after_seconds"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// Domain errors for redundancy
var (
	ErrInstancePeerNotFound      = errors.New("instance peer not found")
	ErrInstancePeerAlreadyExists = errors.New("instance peer already exists")
	ErrInstancePeerInactive      = errors.New("instance peer is inactive")
	ErrInvalidInstanceURL        = errors.New("invalid instance URL")
	ErrRedundancyNotFound        = errors.New("video redundancy not found")
	ErrRedundancyAlreadyExists   = errors.New("video redundancy already exists")
	ErrRedundancyInProgress      = errors.New("redundancy sync is already in progress")
	ErrRedundancyMaxAttempts     = errors.New("maximum sync attempts exceeded")
	ErrRedundancyCancelled       = errors.New("redundancy sync was cancelled")
	ErrInvalidStrategy           = errors.New("invalid redundancy strategy")
	ErrInvalidStatus             = errors.New("invalid redundancy status")
	ErrPolicyNotFound            = errors.New("redundancy policy not found")
	ErrPolicyAlreadyExists       = errors.New("redundancy policy already exists")
	ErrInvalidChecksum           = errors.New("checksum verification failed")
	ErrInsufficientStorage       = errors.New("insufficient storage on target instance")
	ErrInstanceRefusedRedundancy = errors.New("target instance refused redundancy")
)

// Validation methods for InstancePeer

// Validate checks if the instance peer data is valid
func (i *InstancePeer) Validate() error {
	if i.InstanceURL == "" {
		return ErrInvalidInstanceURL
	}

	// Parse and validate URL
	parsedURL, err := url.Parse(i.InstanceURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInstanceURL, err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("%w: scheme must be http or https", ErrInvalidInstanceURL)
	}

	if parsedURL.Host == "" {
		return fmt.Errorf("%w: host is required", ErrInvalidInstanceURL)
	}

	// Set instance host if not set
	if i.InstanceHost == "" {
		i.InstanceHost = parsedURL.Host
	}

	if i.MaxRedundancySizeGB < 0 {
		return errors.New("max redundancy size cannot be negative")
	}

	return nil
}

// CalculateHealthScore returns a health score (0.0-1.0) for the instance
func (i *InstancePeer) CalculateHealthScore() float64 {
	score := 1.0

	// Penalize for failed syncs
	if i.FailedSyncCount > 0 {
		score -= float64(i.FailedSyncCount) * 0.05
	}

	// Penalize if not recently contacted
	if i.LastContactedAt != nil {
		hoursSinceContact := time.Since(*i.LastContactedAt).Hours()
		if hoursSinceContact > 24 {
			score -= 0.1 * (hoursSinceContact / 24.0)
		}
	} else {
		score -= 0.3 // Never contacted
	}

	// Bonus for successful syncs
	if i.LastSyncSuccessAt != nil {
		hoursSinceSuccess := time.Since(*i.LastSyncSuccessAt).Hours()
		if hoursSinceSuccess < 24 {
			score += 0.1
		}
	}

	// Inactive instances get 0 score
	if !i.IsActive {
		return 0.0
	}

	// Clamp to 0.0-1.0
	if score < 0 {
		return 0.0
	}
	if score > 1.0 {
		return 1.0
	}

	return score
}

// HasCapacity checks if the instance can accept more redundancy
func (i *InstancePeer) HasCapacity(videoSizeBytes int64) bool {
	if !i.AcceptsNewRedundancy {
		return false
	}

	if i.MaxRedundancySizeGB == 0 {
		return true // Unlimited
	}

	maxBytes := int64(i.MaxRedundancySizeGB) * 1024 * 1024 * 1024
	return (i.TotalStorageBytes + videoSizeBytes) <= maxBytes
}

// Validation methods for VideoRedundancy

// Validate checks if the video redundancy data is valid
func (v *VideoRedundancy) Validate() error {
	if v.VideoID == "" {
		return errors.New("video ID is required")
	}

	if v.TargetInstanceID == "" {
		return errors.New("target instance ID is required")
	}

	if v.FileSizeBytes < 0 {
		return errors.New("file size cannot be negative")
	}

	if v.BytesTransferred < 0 || v.BytesTransferred > v.FileSizeBytes {
		return errors.New("bytes transferred must be between 0 and file size")
	}

	if v.SyncAttemptCount < 0 {
		return errors.New("sync attempt count cannot be negative")
	}

	if v.MaxSyncAttempts <= 0 {
		return errors.New("max sync attempts must be positive")
	}

	if v.SyncAttemptCount > v.MaxSyncAttempts {
		return ErrRedundancyMaxAttempts
	}

	if v.Priority < 0 {
		return errors.New("priority cannot be negative")
	}

	if !v.IsValidStatus() {
		return ErrInvalidStatus
	}

	if !v.IsValidStrategy() {
		return ErrInvalidStrategy
	}

	return nil
}

// IsValidStatus checks if the status is valid
func (v *VideoRedundancy) IsValidStatus() bool {
	switch v.Status {
	case RedundancyStatusPending, RedundancyStatusSyncing, RedundancyStatusSynced,
		RedundancyStatusFailed, RedundancyStatusCancelled:
		return true
	}
	return false
}

// IsValidStrategy checks if the strategy is valid
func (v *VideoRedundancy) IsValidStrategy() bool {
	switch v.Strategy {
	case RedundancyStrategyRecent, RedundancyStrategyMostViewed,
		RedundancyStrategyTrending, RedundancyStrategyManual, RedundancyStrategyAll:
		return true
	}
	return false
}

// CalculateProgress returns the sync progress as a percentage (0-100)
func (v *VideoRedundancy) CalculateProgress() float64 {
	if v.FileSizeBytes == 0 {
		return 0.0
	}
	return (float64(v.BytesTransferred) / float64(v.FileSizeBytes)) * 100.0
}

// CanRetry checks if the redundancy can be retried
func (v *VideoRedundancy) CanRetry() bool {
	return v.Status == RedundancyStatusFailed && v.SyncAttemptCount < v.MaxSyncAttempts
}

// ShouldResync checks if the redundancy should be resynced (weekly checksum verification)
func (v *VideoRedundancy) ShouldResync() bool {
	if !v.AutoResync || v.Status != RedundancyStatusSynced {
		return false
	}

	if v.ChecksumVerifiedAt == nil {
		return true
	}

	// Resync if last verification was more than 7 days ago
	return time.Since(*v.ChecksumVerifiedAt) > 7*24*time.Hour
}

// MarkSyncing updates the redundancy to syncing status
func (v *VideoRedundancy) MarkSyncing() {
	now := time.Now()
	v.Status = RedundancyStatusSyncing
	v.SyncStartedAt = &now
	v.SyncAttemptCount++
	v.UpdatedAt = now
}

// MarkSynced updates the redundancy to synced status
func (v *VideoRedundancy) MarkSynced(checksum string) {
	now := time.Now()
	v.Status = RedundancyStatusSynced
	v.LastSyncAt = &now
	v.BytesTransferred = v.FileSizeBytes
	v.ChecksumSHA256 = checksum
	v.ChecksumVerifiedAt = &now
	v.SyncError = ""
	v.UpdatedAt = now
}

// MarkFailed updates the redundancy to failed status
func (v *VideoRedundancy) MarkFailed(errMsg string) {
	now := time.Now()
	v.Status = RedundancyStatusFailed
	v.LastSyncAt = &now
	v.SyncError = errMsg
	v.UpdatedAt = now

	// Calculate next sync time with exponential backoff
	if v.SyncAttemptCount < v.MaxSyncAttempts {
		nextSync := calculateNextSyncTime(v.SyncAttemptCount, 60)
		v.NextSyncAt = &nextSync
	}
}

// MarkCancelled updates the redundancy to cancelled status
func (v *VideoRedundancy) MarkCancelled() {
	now := time.Now()
	v.Status = RedundancyStatusCancelled
	v.UpdatedAt = now
}

// Validation methods for RedundancyPolicy

// Validate checks if the policy data is valid
func (p *RedundancyPolicy) Validate() error {
	if p.Name == "" {
		return errors.New("policy name is required")
	}

	if p.TargetInstanceCount < 1 {
		return errors.New("target instance count must be at least 1")
	}

	if p.MinInstanceCount < 1 {
		return errors.New("minimum instance count must be at least 1")
	}

	if p.TargetInstanceCount < p.MinInstanceCount {
		return errors.New("target instance count must be >= minimum instance count")
	}

	if p.MaxAgeDays != nil && *p.MaxAgeDays <= p.MinAgeDays {
		return errors.New("max age days must be greater than min age days")
	}

	if p.EvaluationIntervalHours <= 0 {
		return errors.New("evaluation interval must be positive")
	}

	if !p.IsValidStrategy() {
		return ErrInvalidStrategy
	}

	return nil
}

// IsValidStrategy checks if the strategy is valid
func (p *RedundancyPolicy) IsValidStrategy() bool {
	switch p.Strategy {
	case RedundancyStrategyRecent, RedundancyStrategyMostViewed,
		RedundancyStrategyTrending, RedundancyStrategyManual, RedundancyStrategyAll:
		return true
	}
	return false
}

// ShouldEvaluate checks if the policy should be evaluated now
func (p *RedundancyPolicy) ShouldEvaluate() bool {
	if !p.Enabled {
		return false
	}

	if p.NextEvaluationAt == nil {
		return true
	}

	return time.Now().After(*p.NextEvaluationAt)
}

// CalculateNextEvaluation calculates the next evaluation time
func (p *RedundancyPolicy) CalculateNextEvaluation() time.Time {
	return time.Now().Add(time.Duration(p.EvaluationIntervalHours) * time.Hour)
}

// Helper functions

// calculateNextSyncTime calculates the next sync time with exponential backoff
func calculateNextSyncTime(attemptCount int, baseDelayMinutes int) time.Time {
	// Exponential backoff: 1h, 2h, 4h, 8h, 16h, then cap at 24h
	delayMinutes := baseDelayMinutes
	for i := 0; i < attemptCount && delayMinutes < 1440; i++ {
		delayMinutes *= 2
	}
	if delayMinutes > 1440 {
		delayMinutes = 1440 // Cap at 24 hours
	}

	return time.Now().Add(time.Duration(delayMinutes) * time.Minute)
}

// ValidateRedundancyStatus validates a redundancy status string
func ValidateRedundancyStatus(status string) error {
	switch RedundancyStatus(status) {
	case RedundancyStatusPending, RedundancyStatusSyncing, RedundancyStatusSynced,
		RedundancyStatusFailed, RedundancyStatusCancelled:
		return nil
	}
	return ErrInvalidStatus
}

// ValidateRedundancyStrategy validates a redundancy strategy string
func ValidateRedundancyStrategy(strategy string) error {
	switch RedundancyStrategy(strategy) {
	case RedundancyStrategyRecent, RedundancyStrategyMostViewed,
		RedundancyStrategyTrending, RedundancyStrategyManual, RedundancyStrategyAll:
		return nil
	}
	return ErrInvalidStrategy
}
