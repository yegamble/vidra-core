package domain

import (
	"time"
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
	OriginalCID   string            `json:"original_cid" db:"original_cid"`
	ProcessedCIDs map[string]string `json:"processed_cids" db:"processed_cids"`
	ThumbnailCID  string            `json:"thumbnail_cid" db:"thumbnail_cid"`
	Tags          []string          `json:"tags" db:"tags"`
	Category      string            `json:"category" db:"category"`
	Language      string            `json:"language" db:"language"`
	FileSize      int64             `json:"file_size" db:"file_size"`
	MimeType      string            `json:"mime_type" db:"mime_type"`
	Metadata      VideoMetadata     `json:"metadata" db:"metadata"`
	CreatedAt     time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at" db:"updated_at"`
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
	Query    string   `json:"query"`
	Tags     []string `json:"tags"`
	Category string   `json:"category"`
	Language string   `json:"language"`
	Privacy  Privacy  `json:"privacy"`
	Sort     string   `json:"sort"`
	Order    string   `json:"order"`
	Limit    int      `json:"limit"`
	Offset   int      `json:"offset"`
}

type VideoUploadRequest struct {
	Title       string   `json:"title" validate:"required,min=1,max=255"`
	Description string   `json:"description" validate:"max=5000"`
	Privacy     Privacy  `json:"privacy" validate:"required"`
	Tags        []string `json:"tags" validate:"max=10"`
	Category    string   `json:"category"`
	Language    string   `json:"language"`
}

type VideoUpdateRequest struct {
	Title       string   `json:"title" validate:"required,min=1,max=255"`
	Description string   `json:"description" validate:"max=5000"`
	Privacy     Privacy  `json:"privacy" validate:"required"`
	Tags        []string `json:"tags" validate:"max=10"`
	Category    string   `json:"category"`
	Language    string   `json:"language"`
}

type ChunkUpload struct {
	VideoID     string `json:"video_id"`
	ChunkIndex  int    `json:"chunk_index"`
	TotalChunks int    `json:"total_chunks"`
	Data        []byte `json:"data"`
	Checksum    string `json:"checksum"`
}
