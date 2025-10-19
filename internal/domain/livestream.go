package domain

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Stream status constants
const (
	StreamStatusWaiting = "waiting" // Stream created but not yet started
	StreamStatusLive    = "live"    // Currently broadcasting
	StreamStatusEnded   = "ended"   // Stream has finished
	StreamStatusError   = "error"   // Stream encountered an error
)

// Stream privacy constants
const (
	StreamPrivacyPublic   = "public"
	StreamPrivacyUnlisted = "unlisted"
	StreamPrivacyPrivate  = "private"
)

// LiveStream represents a live streaming session
type LiveStream struct {
	ID              uuid.UUID  `json:"id" db:"id"`
	ChannelID       uuid.UUID  `json:"channel_id" db:"channel_id"`
	UserID          uuid.UUID  `json:"user_id" db:"user_id"`
	Title           string     `json:"title" db:"title"`
	Description     string     `json:"description" db:"description"`
	StreamKey       string     `json:"-" db:"stream_key"` // Never expose in JSON
	Status          string     `json:"status" db:"status"`
	Privacy         string     `json:"privacy" db:"privacy"`
	RTMPURL         string     `json:"rtmp_url" db:"rtmp_url"`
	HLSPlaylistURL  string     `json:"hls_playlist_url" db:"hls_playlist_url"`
	ViewerCount     int        `json:"viewer_count" db:"viewer_count"`
	PeakViewerCount int        `json:"peak_viewer_count" db:"peak_viewer_count"`
	StartedAt       *time.Time `json:"started_at" db:"started_at"`
	EndedAt         *time.Time `json:"ended_at" db:"ended_at"`
	SaveReplay      bool       `json:"save_replay" db:"save_replay"`
	ReplayVideoID   *uuid.UUID `json:"replay_video_id" db:"replay_video_id"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`
}

// StreamKey represents a rotatable authentication key for streaming
type StreamKey struct {
	ID         uuid.UUID  `json:"id" db:"id"`
	ChannelID  uuid.UUID  `json:"channel_id" db:"channel_id"`
	KeyHash    string     `json:"-" db:"key_hash"` // Bcrypt hash, never exposed
	LastUsedAt *time.Time `json:"last_used_at" db:"last_used_at"`
	IsActive   bool       `json:"is_active" db:"is_active"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at" db:"expires_at"`
}

// ViewerSession represents an individual viewer watching a live stream
type ViewerSession struct {
	ID              uuid.UUID  `json:"id" db:"id"`
	LiveStreamID    uuid.UUID  `json:"live_stream_id" db:"live_stream_id"`
	SessionID       string     `json:"session_id" db:"session_id"`
	UserID          *uuid.UUID `json:"user_id" db:"user_id"`
	IPAddress       string     `json:"ip_address" db:"ip_address"`
	UserAgent       string     `json:"user_agent" db:"user_agent"`
	CountryCode     string     `json:"country_code" db:"country_code"`
	JoinedAt        time.Time  `json:"joined_at" db:"joined_at"`
	LeftAt          *time.Time `json:"left_at" db:"left_at"`
	LastHeartbeatAt time.Time  `json:"last_heartbeat_at" db:"last_heartbeat_at"`
}

// Domain errors for live streaming
var (
	ErrStreamNotFound        = errors.New("live stream not found")
	ErrStreamKeyInvalid      = errors.New("invalid stream key")
	ErrStreamKeyExpired      = errors.New("stream key has expired")
	ErrStreamKeyInactive     = errors.New("stream key is not active")
	ErrStreamAlreadyLive     = errors.New("stream is already live")
	ErrStreamNotLive         = errors.New("stream is not currently live")
	ErrStreamEnded           = errors.New("stream has already ended")
	ErrInvalidStreamStatus   = errors.New("invalid stream status")
	ErrInvalidStreamPrivacy  = errors.New("invalid stream privacy setting")
	ErrMaxConcurrentStreams  = errors.New("maximum concurrent streams reached")
	ErrStreamTitleRequired   = errors.New("stream title is required")
	ErrStreamTitleTooLong    = errors.New("stream title exceeds maximum length")
	ErrViewerSessionNotFound = errors.New("viewer session not found")
)

// Validation methods

// Validate checks if the LiveStream has valid data
func (ls *LiveStream) Validate() error {
	if ls.Title == "" {
		return ErrStreamTitleRequired
	}
	if len(ls.Title) > 255 {
		return ErrStreamTitleTooLong
	}
	if !isValidStreamStatus(ls.Status) {
		return ErrInvalidStreamStatus
	}
	if !isValidPrivacy(ls.Privacy) {
		return ErrInvalidStreamPrivacy
	}
	return nil
}

// IsLive returns true if the stream is currently broadcasting
func (ls *LiveStream) IsLive() bool {
	return ls.Status == StreamStatusLive
}

// IsEnded returns true if the stream has finished
func (ls *LiveStream) IsEnded() bool {
	return ls.Status == StreamStatusEnded
}

// CanStart returns true if the stream can transition to live status
func (ls *LiveStream) CanStart() bool {
	return ls.Status == StreamStatusWaiting
}

// Duration returns the stream duration if it has started
func (ls *LiveStream) Duration() time.Duration {
	if ls.StartedAt == nil {
		return 0
	}
	endTime := time.Now()
	if ls.EndedAt != nil {
		endTime = *ls.EndedAt
	}
	return endTime.Sub(*ls.StartedAt)
}

// Start transitions the stream to live status
func (ls *LiveStream) Start() error {
	if !ls.CanStart() {
		return ErrStreamAlreadyLive
	}
	now := time.Now()
	ls.Status = StreamStatusLive
	ls.StartedAt = &now
	return nil
}

// End transitions the stream to ended status
func (ls *LiveStream) End() error {
	if !ls.IsLive() {
		return ErrStreamNotLive
	}
	now := time.Now()
	ls.Status = StreamStatusEnded
	ls.EndedAt = &now
	ls.ViewerCount = 0
	return nil
}

// UpdateViewerCount updates the current and peak viewer counts
func (ls *LiveStream) UpdateViewerCount(count int) {
	ls.ViewerCount = count
	if count > ls.PeakViewerCount {
		ls.PeakViewerCount = count
	}
}

// StreamKey generation and validation

// GenerateStreamKey creates a cryptographically secure random stream key
func GenerateStreamKey() (string, error) {
	// Generate 32 bytes of random data
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate stream key: %w", err)
	}
	// Encode as URL-safe base64
	return base64.URLEncoding.EncodeToString(b), nil
}

// IsExpired checks if the stream key has expired
func (sk *StreamKey) IsExpired() bool {
	if sk.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*sk.ExpiresAt)
}

// CanUse checks if the stream key can be used for streaming
func (sk *StreamKey) CanUse() error {
	if !sk.IsActive {
		return ErrStreamKeyInactive
	}
	if sk.IsExpired() {
		return ErrStreamKeyExpired
	}
	return nil
}

// ViewerSession methods

// IsActive returns true if the viewer session is currently active
func (vs *ViewerSession) IsActive() bool {
	return vs.LeftAt == nil
}

// WatchDuration returns how long the viewer has been watching
func (vs *ViewerSession) WatchDuration() time.Duration {
	endTime := time.Now()
	if vs.LeftAt != nil {
		endTime = *vs.LeftAt
	}
	return endTime.Sub(vs.JoinedAt)
}

// NeedsHeartbeat returns true if the session needs a heartbeat update
func (vs *ViewerSession) NeedsHeartbeat() bool {
	return time.Since(vs.LastHeartbeatAt) > 20*time.Second
}

// UpdateHeartbeat updates the last heartbeat timestamp
func (vs *ViewerSession) UpdateHeartbeat() {
	vs.LastHeartbeatAt = time.Now()
}

// Helper validation functions

func isValidStreamStatus(status string) bool {
	switch status {
	case StreamStatusWaiting, StreamStatusLive, StreamStatusEnded, StreamStatusError:
		return true
	default:
		return false
	}
}

func isValidPrivacy(privacy string) bool {
	switch privacy {
	case StreamPrivacyPublic, StreamPrivacyUnlisted, StreamPrivacyPrivate:
		return true
	default:
		return false
	}
}

// StreamStats represents aggregated statistics for a stream
type StreamStats struct {
	StreamID         uuid.UUID     `json:"stream_id"`
	TotalViewers     int           `json:"total_viewers"`
	UniqueViewers    int           `json:"unique_viewers"`
	CurrentViewers   int           `json:"current_viewers"`
	PeakViewers      int           `json:"peak_viewers"`
	AverageWatchTime time.Duration `json:"average_watch_time"`
	TotalWatchTime   time.Duration `json:"total_watch_time"`
	Duration         time.Duration `json:"duration"`
}

// CreateLiveStreamParams represents parameters for creating a new live stream
type CreateLiveStreamParams struct {
	ChannelID   uuid.UUID
	UserID      uuid.UUID
	Title       string
	Description string
	Privacy     string
	SaveReplay  bool
}

// Validate checks if the creation parameters are valid
func (p *CreateLiveStreamParams) Validate() error {
	if p.Title == "" {
		return ErrStreamTitleRequired
	}
	if len(p.Title) > 255 {
		return ErrStreamTitleTooLong
	}
	if !isValidPrivacy(p.Privacy) {
		return ErrInvalidStreamPrivacy
	}
	return nil
}
