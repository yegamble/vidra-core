package model

import "time"

// VideoStatus enumerates the various states a video can be in. These
// constants help avoid typos in the codebase and clearly express the
// lifecycle of a video: waiting for processing, actively processing,
// ready for consumption, or failed.
const (
    VideoStatusPending    = "pending"    // waiting for transcoding
    VideoStatusProcessing = "processing" // currently being transcoded
    VideoStatusReady      = "ready"      // all renditions available
    VideoStatusFailed     = "failed"     // processing failed
)

// Video represents a video uploaded by a user. Each upload has a unique
// identifier, belongs to a user, and includes metadata as well as a
// reference to the original file and its IPFS CID. The status field
// indicates the current processing state.
type Video struct {
    ID             int64     `db:"id" json:"id"`
    UserID         int64     `db:"user_id" json:"user_id"`
    Title          string    `db:"title" json:"title"`
    Description    string    `db:"description" json:"description"`
    OriginalName   string    `db:"original_name" json:"original_name"`
    IPFSCID        string    `db:"ipfs_cid" json:"ipfs_cid"`
    Status         string    `db:"status" json:"status"`
    CreatedAt      time.Time `db:"created_at" json:"created_at"`
    UpdatedAt      time.Time `db:"updated_at" json:"updated_at"`
}

// VideoRendition represents one encoded version of a video at a specific
// resolution and bitrate. Each rendition belongs to a video and stores
// the path or URL where the encoded file can be accessed. The created
// timestamp is recorded for potential debugging or auditing.
type VideoRendition struct {
    ID        int64     `db:"id" json:"id"`
    VideoID   int64     `db:"video_id" json:"video_id"`
    Resolution string    `db:"resolution" json:"resolution"`
    Bitrate   int        `db:"bitrate" json:"bitrate"`
    FilePath  string    `db:"file_path" json:"file_path"`
    CreatedAt time.Time `db:"created_at" json:"created_at"`
}