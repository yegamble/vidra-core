package model

import "time"

// User represents a registered user in the system
type User struct {
	ID           int64     `db:"id" json:"id"`
	Email        string    `db:"email" json:"email"`
	PasswordHash string    `db:"password_hash" json:"-"`
	Verified     bool      `db:"verified" json:"verified"`
	IotaWallet   string    `db:"iota_wallet" json:"iota_wallet"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

// VideoStatus enumerates the various states a video can be in
const (
	VideoStatusPending    = "pending"    // waiting for transcoding
	VideoStatusProcessing = "processing" // currently being transcoded
	VideoStatusReady      = "ready"      // all renditions available
	VideoStatusFailed     = "failed"     // processing failed
)

// Video represents a video uploaded by a user
type Video struct {
	ID           int64     `db:"id" json:"id"`
	UserID       int64     `db:"user_id" json:"user_id"`
	Title        string    `db:"title" json:"title"`
	Description  string    `db:"description" json:"description"`
	OriginalName string    `db:"original_name" json:"original_name"`
	IPFSCID      string    `db:"ipfs_cid" json:"ipfs_cid"`
	Status       string    `db:"status" json:"status"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

// VideoRendition represents one encoded version of a video
type VideoRendition struct {
	ID         int64     `db:"id" json:"id"`
	VideoID    int64     `db:"video_id" json:"video_id"`
	Resolution string    `db:"resolution" json:"resolution"`
	Bitrate    int       `db:"bitrate" json:"bitrate"`
	FilePath   string    `db:"file_path" json:"file_path"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
}

// IPFSContent represents IPFS stored content
type IPFSContent struct {
	ID         int64     `db:"id" json:"id"`
	VideoID    int64     `db:"video_id" json:"video_id"`
	CID        string    `db:"cid" json:"cid"`
	FileType   string    `db:"file_type" json:"file_type"`
	Resolution int       `db:"resolution" json:"resolution"`
	FileSize   int64     `db:"file_size" json:"file_size"`
	PinStatus  string    `db:"pin_status" json:"pin_status"`
	GatewayURL string    `db:"gateway_url" json:"gateway_url"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
}
