package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ImportStatus represents the status of a video import
type ImportStatus string

const (
	ImportStatusPending     ImportStatus = "pending"
	ImportStatusDownloading ImportStatus = "downloading"
	ImportStatusProcessing  ImportStatus = "processing"
	ImportStatusCompleted   ImportStatus = "completed"
	ImportStatusFailed      ImportStatus = "failed"
	ImportStatusCancelled   ImportStatus = "cancelled"
)

// IsTerminal returns true if the import status is in a terminal state
func (s ImportStatus) IsTerminal() bool {
	return s == ImportStatusCompleted || s == ImportStatusFailed || s == ImportStatusCancelled
}

// VideoImport represents a video import from an external source
type VideoImport struct {
	ID              string          `db:"id" json:"id"`
	UserID          string          `db:"user_id" json:"user_id"`
	ChannelID       *string         `db:"channel_id" json:"channel_id,omitempty"`
	SourceURL       string          `db:"source_url" json:"source_url"`
	Status          ImportStatus    `db:"status" json:"status"`
	VideoID         *string         `db:"video_id" json:"video_id,omitempty"`
	ErrorMessage    *string         `db:"error_message" json:"error_message,omitempty"`
	Progress        int             `db:"progress" json:"progress"`
	Metadata        json.RawMessage `db:"metadata" json:"metadata,omitempty"`
	FileSizeBytes   *int64          `db:"file_size_bytes" json:"file_size_bytes,omitempty"`
	DownloadedBytes int64           `db:"downloaded_bytes" json:"downloaded_bytes"`
	TargetPrivacy   string          `db:"target_privacy" json:"target_privacy"`
	TargetCategory  *string         `db:"target_category" json:"target_category,omitempty"`
	CreatedAt       time.Time       `db:"created_at" json:"created_at"`
	StartedAt       *time.Time      `db:"started_at" json:"started_at,omitempty"`
	CompletedAt     *time.Time      `db:"completed_at" json:"completed_at,omitempty"`
	UpdatedAt       time.Time       `db:"updated_at" json:"updated_at"`
	MetadataParsed  *ImportMetadata `db:"-" json:"metadata_parsed,omitempty"`
}

// ImportMetadata represents metadata extracted from the source video
type ImportMetadata struct {
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Duration       int      `json:"duration"` // Duration in seconds
	Uploader       string   `json:"uploader"`
	UploaderURL    string   `json:"uploader_url"`
	ThumbnailURL   string   `json:"thumbnail"`
	ViewCount      int64    `json:"view_count"`
	LikeCount      int64    `json:"like_count"`
	Tags           []string `json:"tags"`
	Categories     []string `json:"categories"`
	UploadDate     string   `json:"upload_date"`   // Format: YYYYMMDD
	ExtractorKey   string   `json:"extractor_key"` // e.g., "Youtube", "Vimeo"
	Format         string   `json:"format"`
	FormatID       string   `json:"format_id"`
	Width          int      `json:"width"`
	Height         int      `json:"height"`
	FPS            float64  `json:"fps"`
	VideoCodec     string   `json:"vcodec"`
	AudioCodec     string   `json:"acodec"`
	Filesize       int64    `json:"filesize"`
	FilesizeApprox int64    `json:"filesize_approx"`
}

// Validate validates the video import fields
func (vi *VideoImport) Validate() error {
	if vi.UserID == "" {
		return errors.New("user_id is required")
	}
	if vi.SourceURL == "" {
		return errors.New("source_url is required")
	}
	if err := ValidateURL(vi.SourceURL); err != nil {
		return fmt.Errorf("invalid source_url: %w", err)
	}
	if vi.Progress < 0 || vi.Progress > 100 {
		return fmt.Errorf("progress must be between 0 and 100, got %d", vi.Progress)
	}
	if vi.TargetPrivacy != "" {
		if err := ValidatePrivacy(vi.TargetPrivacy); err != nil {
			return fmt.Errorf("invalid target_privacy: %w", err)
		}
	}
	return nil
}

// ValidateURL validates that a URL is well-formed and uses http/https
func ValidateURL(rawURL string) error {
	if rawURL == "" {
		return errors.New("URL cannot be empty")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL must use http or https scheme, got: %s", u.Scheme)
	}

	if u.Host == "" {
		return errors.New("URL must have a host")
	}

	return nil
}

// ValidatePrivacy validates a privacy value
func ValidatePrivacy(privacy string) error {
	validPrivacy := map[string]bool{
		string(PrivacyPublic):   true,
		string(PrivacyUnlisted): true,
		string(PrivacyPrivate):  true,
	}

	if !validPrivacy[privacy] {
		return fmt.Errorf("invalid privacy value: %s (must be public, unlisted, or private)", privacy)
	}

	return nil
}

// CanTransition checks if a status transition is valid
func (vi *VideoImport) CanTransition(newStatus ImportStatus) bool {
	// Terminal states cannot transition
	if vi.Status.IsTerminal() {
		return false
	}

	// Valid transitions
	validTransitions := map[ImportStatus][]ImportStatus{
		ImportStatusPending: {
			ImportStatusDownloading,
			ImportStatusCancelled,
			ImportStatusFailed,
		},
		ImportStatusDownloading: {
			ImportStatusProcessing,
			ImportStatusFailed,
			ImportStatusCancelled,
		},
		ImportStatusProcessing: {
			ImportStatusCompleted,
			ImportStatusFailed,
		},
	}

	allowed, exists := validTransitions[vi.Status]
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

// Start marks the import as started
func (vi *VideoImport) Start() error {
	if !vi.CanTransition(ImportStatusDownloading) {
		return fmt.Errorf("cannot transition from %s to downloading", vi.Status)
	}

	now := time.Now()
	vi.Status = ImportStatusDownloading
	vi.StartedAt = &now
	vi.UpdatedAt = now

	return nil
}

// MarkProcessing marks the import as processing (encoding)
func (vi *VideoImport) MarkProcessing() error {
	if !vi.CanTransition(ImportStatusProcessing) {
		return fmt.Errorf("cannot transition from %s to processing", vi.Status)
	}

	vi.Status = ImportStatusProcessing
	vi.UpdatedAt = time.Now()

	return nil
}

// Complete marks the import as completed
func (vi *VideoImport) Complete(videoID string) error {
	if !vi.CanTransition(ImportStatusCompleted) {
		return fmt.Errorf("cannot transition from %s to completed", vi.Status)
	}

	now := time.Now()
	vi.Status = ImportStatusCompleted
	vi.VideoID = &videoID
	vi.CompletedAt = &now
	vi.UpdatedAt = now
	vi.Progress = 100

	return nil
}

// Fail marks the import as failed
func (vi *VideoImport) Fail(errorMessage string) error {
	if vi.Status.IsTerminal() && vi.Status != ImportStatusFailed {
		return fmt.Errorf("cannot transition from terminal status %s to failed", vi.Status)
	}

	vi.Status = ImportStatusFailed
	vi.ErrorMessage = &errorMessage
	vi.UpdatedAt = time.Now()

	return nil
}

// Cancel marks the import as cancelled
func (vi *VideoImport) Cancel() error {
	if vi.Status.IsTerminal() {
		return fmt.Errorf("cannot cancel import in terminal status: %s", vi.Status)
	}

	vi.Status = ImportStatusCancelled
	vi.UpdatedAt = time.Now()

	return nil
}

// UpdateProgress updates the download progress
func (vi *VideoImport) UpdateProgress(progress int, downloadedBytes int64) error {
	if progress < 0 || progress > 100 {
		return fmt.Errorf("progress must be between 0 and 100, got %d", progress)
	}

	if downloadedBytes < 0 {
		return fmt.Errorf("downloaded_bytes cannot be negative")
	}

	vi.Progress = progress
	vi.DownloadedBytes = downloadedBytes
	vi.UpdatedAt = time.Now()

	return nil
}

// SetMetadata sets the import metadata from yt-dlp output
func (vi *VideoImport) SetMetadata(metadata *ImportMetadata) error {
	if metadata == nil {
		return errors.New("metadata cannot be nil")
	}

	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	vi.Metadata = data
	vi.MetadataParsed = metadata

	// Set file size if available
	if metadata.Filesize > 0 {
		vi.FileSizeBytes = &metadata.Filesize
	} else if metadata.FilesizeApprox > 0 {
		vi.FileSizeBytes = &metadata.FilesizeApprox
	}

	return nil
}

// GetMetadata parses and returns the metadata
func (vi *VideoImport) GetMetadata() (*ImportMetadata, error) {
	if vi.MetadataParsed != nil {
		return vi.MetadataParsed, nil
	}

	if len(vi.Metadata) == 0 {
		return nil, errors.New("no metadata available")
	}

	var metadata ImportMetadata
	if err := json.Unmarshal(vi.Metadata, &metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	vi.MetadataParsed = &metadata
	return &metadata, nil
}

// GetSourcePlatform extracts the platform name from the source URL
func (vi *VideoImport) GetSourcePlatform() string {
	u, err := url.Parse(vi.SourceURL)
	if err != nil || u.Host == "" {
		return "unknown"
	}

	host := strings.ToLower(u.Host)

	switch {
	case strings.Contains(host, "youtube.com") || strings.Contains(host, "youtu.be"):
		return "YouTube"
	case strings.Contains(host, "vimeo.com"):
		return "Vimeo"
	case strings.Contains(host, "dailymotion.com"):
		return "Dailymotion"
	case strings.Contains(host, "twitch.tv"):
		return "Twitch"
	case strings.Contains(host, "twitter.com") || strings.Contains(host, "x.com"):
		return "Twitter"
	default:
		return host
	}
}

// Domain errors for imports
var (
	ErrImportNotFound         = errors.New("import not found")
	ErrImportInvalidURL       = errors.New("invalid import URL")
	ErrImportAlreadyExists    = errors.New("import already exists")
	ErrImportStatusInvalid    = errors.New("invalid import status transition")
	ErrImportQuotaExceeded    = errors.New("import quota exceeded")
	ErrImportRateLimited      = errors.New("import rate limited")
	ErrImportDownloadFailed   = errors.New("download failed")
	ErrImportProcessingFailed = errors.New("processing failed")
	ErrImportCancelled        = errors.New("import was cancelled")
	ErrImportUnsupportedURL   = errors.New("unsupported URL or platform")
)
