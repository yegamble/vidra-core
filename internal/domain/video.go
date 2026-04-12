package domain

import (
	"math"
	"time"

	"github.com/google/uuid"
)

const DefaultResolution = "720p"

var SupportedResolutions = []string{
	"240p",
	"360p",
	"480p",
	"720p",
	"1080p",
	"1440p",
	"2160p",
	"4320p",
}

var supportedResolutionSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(SupportedResolutions))
	for _, r := range SupportedResolutions {
		m[r] = struct{}{}
	}
	return m
}()

func IsValidResolution(res string) bool {
	_, ok := supportedResolutionSet[res]
	return ok
}

// HeightForResolution returns the pixel height for a given resolution label
// and a boolean indicating whether it exists.
func HeightForResolution(res string) (int, bool) {
	h, ok := ResolutionHeights[res]
	return h, ok
}

type Video struct {
	ID            string            `json:"id" db:"id"`
	ThumbnailID   string            `json:"thumbnail_id" db:"thumbnail_id"`
	Title         string            `json:"title" db:"title"`
	Description   string            `json:"description" db:"description"`
	Duration      int               `json:"duration" db:"duration"`
	Views         int64             `json:"views" db:"views"`
	Privacy       Privacy           `json:"privacy" db:"privacy"`
	Status        ProcessingStatus  `json:"status" db:"status"`
	UploadDate    time.Time         `json:"upload_date" db:"upload_date"`
	UserID        string            `json:"user_id" db:"user_id"`
	ChannelID     uuid.UUID         `json:"channel_id" db:"channel_id"`
	OriginalCID   string            `json:"original_cid" db:"original_cid"`
	ProcessedCIDs map[string]string `json:"processed_cids" db:"processed_cids"`
	ThumbnailCID  string            `json:"thumbnail_cid" db:"thumbnail_cid"`
	OutputPaths   map[string]string `json:"output_paths" db:"output_paths"`
	S3URLs        map[string]string `json:"s3_urls" db:"s3_urls"`
	StorageTier   string            `json:"storage_tier" db:"storage_tier"`
	S3MigratedAt  *time.Time        `json:"s3_migrated_at" db:"s3_migrated_at"`
	LocalDeleted  bool              `json:"local_deleted" db:"local_deleted"`
	ThumbnailPath string            `json:"thumbnail_path" db:"thumbnail_path"`
	PreviewPath   string            `json:"preview_path" db:"preview_path"`
	Tags          []string          `json:"tags" db:"tags"`
	CategoryID    *uuid.UUID        `json:"category_id" db:"category_id"`
	Category      *VideoCategory    `json:"category,omitempty" db:"-"`
	Channel       *Channel          `json:"channel,omitempty" db:"-"`
	Language      string            `json:"language" db:"language"`
	FileSize      int64             `json:"file_size" db:"file_size"`
	MimeType      string            `json:"mime_type" db:"mime_type"`
	Metadata      VideoMetadata     `json:"metadata" db:"metadata"`
	// Remote video fields (for federated videos from other instances)
	IsRemote             bool       `json:"is_remote" db:"is_remote"`
	RemoteURI            *string    `json:"remote_uri,omitempty" db:"remote_uri"`
	RemoteActorURI       *string    `json:"remote_actor_uri,omitempty" db:"remote_actor_uri"`
	RemoteVideoURL       *string    `json:"remote_video_url,omitempty" db:"remote_video_url"`
	RemoteInstanceDomain *string    `json:"remote_instance_domain,omitempty" db:"remote_instance_domain"`
	RemoteThumbnailURL   *string    `json:"remote_thumbnail_url,omitempty" db:"remote_thumbnail_url"`
	RemoteLastSyncedAt   *time.Time `json:"remote_last_synced_at,omitempty" db:"remote_last_synced_at"`
	WaitTranscoding      bool       `json:"wait_transcoding" db:"wait_transcoding"`
	CreatedAt            time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at" db:"updated_at"`
	// Computed field for PeerTube v8.1 compat — populated by ComputeThumbnails()
	Thumbnails []VideoThumbnail `json:"thumbnails,omitempty" db:"-"`
}

// VideoResolution identifies a video resolution for PeerTube-compatible API responses.
type VideoResolution struct {
	ID    int    `json:"id"`    // Height in pixels (e.g., 720)
	Label string `json:"label"` // Human-readable label (e.g., "720p")
}

// VideoFile represents a single video file in PeerTube-compatible API responses.
type VideoFile struct {
	Resolution      VideoResolution `json:"resolution"`
	MagnetUri       string          `json:"magnetUri,omitempty"`
	InfoHash        string          `json:"infoHash,omitempty"`
	Size            int64           `json:"size,omitempty"`
	Fps             float64         `json:"fps,omitempty"`
	FileUrl         string          `json:"fileUrl"`
	FileDownloadUrl string          `json:"fileDownloadUrl,omitempty"`
}

// StreamingPlaylist represents an HLS streaming playlist in PeerTube-compatible API responses.
type StreamingPlaylist struct {
	Type             int         `json:"type"` // 1 = HLS
	PlaylistUrl      string      `json:"playlistUrl"`
	SegmentsSha256Url string     `json:"segmentsSha256Url,omitempty"`
	Files            []VideoFile `json:"files"`
	RedundancyUrls   []string    `json:"redundancies,omitempty"`
}

// VideoThumbnail represents an entry in the PeerTube v8.1 thumbnails array.
type VideoThumbnail struct {
	Type   string `json:"type"`
	Path   string `json:"path"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// ComputeThumbnails populates the Thumbnails array from ThumbnailPath and PreviewPath.
func (v *Video) ComputeThumbnails() {
	v.Thumbnails = nil
	if v.ThumbnailPath != "" {
		v.Thumbnails = append(v.Thumbnails, VideoThumbnail{
			Type:   "thumbnail",
			Path:   v.ThumbnailPath,
			Width:  280,
			Height: 157,
		})
	}
	if v.PreviewPath != "" {
		v.Thumbnails = append(v.Thumbnails, VideoThumbnail{
			Type:   "preview",
			Path:   v.PreviewPath,
			Width:  850,
			Height: 480,
		})
	}
}

type VideoMetadata struct {
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	Framerate   float64 `json:"framerate"`
	Bitrate     int     `json:"bitrate"`
	AudioCodec  string  `json:"audio_codec"`
	VideoCodec  string  `json:"video_codec"`
	AspectRatio string  `json:"aspect_ratio"`
}

type Privacy string

const (
	PrivacyPublic   Privacy = "public"
	PrivacyUnlisted Privacy = "unlisted"
	PrivacyPrivate  Privacy = "private"
)

type ProcessingStatus string

const (
	StatusUploading  ProcessingStatus = "uploading"
	StatusQueued     ProcessingStatus = "queued"
	StatusProcessing ProcessingStatus = "processing"
	StatusCompleted  ProcessingStatus = "completed"
	StatusFailed     ProcessingStatus = "failed"
)

type VideoSearchRequest struct {
	Query      string     `json:"query"`
	Tags       []string   `json:"tags"`
	CategoryID *uuid.UUID `json:"category_id"`
	ChannelID  *uuid.UUID `json:"channel_id"`
	AccountID  *uuid.UUID `json:"account_id"`
	Language   string     `json:"language"`
	Host       string     `json:"host"` // PeerTube v7.0: filter by instance domain
	Privacy    Privacy    `json:"privacy"`
	Sort       string     `json:"sort"`
	Order      string     `json:"order"`
	Limit      int        `json:"limit"`
	Offset     int        `json:"offset"`
}

type VideoUploadRequest struct {
	Title           string     `json:"title" validate:"required,min=1,max=255"`
	Description     string     `json:"description" validate:"max=5000"`
	Privacy         Privacy    `json:"privacy" validate:"required"`
	Tags            []string   `json:"tags" validate:"max=10"`
	CategoryID      *uuid.UUID `json:"category_id" validate:"omitempty"`
	Language        string     `json:"language"`
	WaitTranscoding bool       `json:"waitTranscoding"` // PeerTube parity: if true, video not published until encoding completes
}

type VideoUpdateRequest struct {
	Title       string     `json:"title" validate:"required,min=1,max=255"`
	Description string     `json:"description" validate:"max=5000"`
	Privacy     Privacy    `json:"privacy" validate:"required"`
	Tags        []string   `json:"tags" validate:"max=10"`
	CategoryID  *uuid.UUID `json:"category_id" validate:"omitempty"`
	Language    string     `json:"language"`
}

// Upload tracking models
type UploadSession struct {
	ID               string       `json:"id" db:"id"`
	VideoID          string       `json:"video_id" db:"video_id"`
	UserID           string       `json:"user_id" db:"user_id"`
	BatchID          *string      `json:"batch_id,omitempty" db:"batch_id"`
	FileName         string       `json:"filename" db:"filename"`
	FileSize         int64        `json:"file_size" db:"file_size"`
	ChunkSize        int64        `json:"chunk_size" db:"chunk_size"`
	TotalChunks      int          `json:"total_chunks" db:"total_chunks"`
	UploadedChunks   []int        `json:"uploaded_chunks" db:"uploaded_chunks"`
	Status           UploadStatus `json:"status" db:"status"`
	TempFilePath     string       `json:"temp_file_path" db:"temp_file_path"`
	ExpectedChecksum string       `json:"expected_checksum" db:"expected_checksum"`
	CreatedAt        time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at" db:"updated_at"`
	ExpiresAt        time.Time    `json:"expires_at" db:"expires_at"`
}

type ChunkUpload struct {
	SessionID  string `json:"session_id"`
	ChunkIndex int    `json:"chunk_index"`
	Data       []byte `json:"data"`
	Checksum   string `json:"checksum"`
}

type UploadStatus string

const (
	UploadStatusActive    UploadStatus = "active"
	UploadStatusCompleted UploadStatus = "completed"
	UploadStatusExpired   UploadStatus = "expired"
	UploadStatusFailed    UploadStatus = "failed"
)

// VideoListResponse represents a paginated list of videos
type VideoListResponse struct {
	Total    int     `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"pageSize"`
	Data     []Video `json:"data"`
}

// Encoding queue models
type EncodingJob struct {
	ID                string         `json:"id" db:"id"`
	VideoID           string         `json:"video_id" db:"video_id"`
	SourceFilePath    string         `json:"source_file_path" db:"source_file_path"`
	SourceResolution  string         `json:"source_resolution" db:"source_resolution"`
	TargetResolutions []string       `json:"target_resolutions" db:"target_resolutions"`
	Status            EncodingStatus `json:"status" db:"status"`
	Progress          int            `json:"progress" db:"progress"` // 0-100
	CurrentResolution string         `json:"current_resolution,omitempty" db:"current_resolution"`
	Duration          int            `json:"duration,omitempty" db:"duration"` // seconds
	ErrorMessage      string         `json:"error_message" db:"error_message"`
	StartedAt         *time.Time     `json:"started_at" db:"started_at"`
	CompletedAt       *time.Time     `json:"completed_at" db:"completed_at"`
	CreatedAt         time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at" db:"updated_at"`
}

type EncodingStatus string

const (
	EncodingStatusPending    EncodingStatus = "pending"
	EncodingStatusProcessing EncodingStatus = "processing"
	EncodingStatusCompleted  EncodingStatus = "completed"
	EncodingStatusFailed     EncodingStatus = "failed"
)

// Resolution mapping for encoding decisions
var ResolutionHeights = map[string]int{
	"240p":  240,
	"360p":  360,
	"480p":  480,
	"720p":  720,
	"1080p": 1080,
	"1440p": 1440,
	"2160p": 2160,
	"4320p": 4320,
}

// GetTargetResolutions returns which resolutions to encode based on source
func GetTargetResolutions(sourceResolution string) []string {
	sourceHeight, exists := ResolutionHeights[sourceResolution]
	if !exists {
		// Default fallback
		return []string{"720p", "480p", "360p", "240p"}
	}

	var targets []string
	for _, resolution := range SupportedResolutions {
		if height := ResolutionHeights[resolution]; height <= sourceHeight {
			targets = append(targets, resolution)
		}
	}

	// Always include at least 240p
	if len(targets) == 0 {
		targets = []string{"240p"}
	}

	return targets
}

// DetectResolutionFromHeight converts pixel height to resolution string
func DetectResolutionFromHeight(height int) string {
	// Find the closest standard resolution deterministically and
	// prefer the lower resolution when distances tie.
	bestMatch := "240p"
	smallestDiff := abs(height - ResolutionHeights[bestMatch])

	for _, resolution := range SupportedResolutions {
		standardHeight := ResolutionHeights[resolution]
		diff := abs(height - standardHeight)
		if diff < smallestDiff {
			smallestDiff = diff
			bestMatch = resolution
			continue
		}
		// On exact tie, prefer lower (smaller height)
		if diff == smallestDiff {
			currentBestHeight := ResolutionHeights[bestMatch]
			if standardHeight < currentBestHeight {
				bestMatch = resolution
			}
		}
	}

	return bestMatch
}

func abs(x int) int {
	if x == math.MinInt {
		return math.MaxInt
	}
	if x < 0 {
		return -x
	}
	return x
}

// Upload request models
type InitiateUploadRequest struct {
	FileName         string `json:"filename" validate:"required"`
	FileSize         int64  `json:"file_size" validate:"required,min=1"`
	ChunkSize        int64  `json:"chunk_size" validate:"min=1048576,max=104857600"` // 1MB to 100MB
	ExpectedChecksum string `json:"expected_checksum"`
	WaitTranscoding  bool   `json:"waitTranscoding"` // PeerTube parity: if true, video not published until encoding completes
}

type InitiateUploadResponse struct {
	SessionID   string `json:"session_id"`
	ChunkSize   int64  `json:"chunk_size"`
	TotalChunks int    `json:"total_chunks"`
	UploadURL   string `json:"upload_url"`
}

type ChunkUploadResponse struct {
	ChunkIndex      int   `json:"chunk_index"`
	Uploaded        bool  `json:"uploaded"`
	RemainingChunks []int `json:"remaining_chunks"`
}

// Batch upload models

// BatchUpload tracks a group of upload sessions initiated together.
type BatchUpload struct {
	ID          string    `json:"id" db:"id"`
	UserID      string    `json:"user_id" db:"user_id"`
	TotalVideos int       `json:"total_videos" db:"total_videos"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// BatchUploadVideoItem describes a single video in a batch upload request.
type BatchUploadVideoItem struct {
	FileName    string `json:"filename" validate:"required"`
	FileSize    int64  `json:"file_size" validate:"required,min=1"`
	ChunkSize   int64  `json:"chunk_size"`
	Title       string `json:"title" validate:"required"`
	Description string `json:"description"`
	Privacy     string `json:"privacy"`
}

// BatchUploadRequest is the request body for initiating a batch upload.
type BatchUploadRequest struct {
	Videos []BatchUploadVideoItem `json:"videos" validate:"required,min=1"`
}

// BatchUploadResponse is returned after successfully initiating a batch upload.
type BatchUploadResponse struct {
	BatchID  string                   `json:"batch_id"`
	Sessions []InitiateUploadResponse `json:"sessions"`
}

// BatchUploadStatus reports the progress of a batch upload.
type BatchUploadStatus struct {
	BatchID          string          `json:"batch_id"`
	TotalVideos      int             `json:"total_videos"`
	CompletedUploads int             `json:"completed_uploads"`
	ActiveUploads    int             `json:"active_uploads"`
	FailedUploads    int             `json:"failed_uploads"`
	Sessions         []UploadSession `json:"sessions"`
}

// VideoChapter represents a chapter marker within a video (timestamp + title).
type VideoChapter struct {
	ID       string `json:"id,omitempty" db:"id"`
	VideoID  string `json:"video_id" db:"video_id"`
	Timecode int    `json:"timecode" db:"timecode"` // seconds from start
	Title    string `json:"title" db:"title"`
	Position int    `json:"position" db:"position"` // display order
}
