package domain

import (
	"time"

	"github.com/google/uuid"
)

// Channel represents a content channel that owns videos
// Based on PeerTube's channel model where channels are separate from user accounts
type Channel struct {
	ID             uuid.UUID `json:"id" db:"id"`
	AccountID      uuid.UUID `json:"accountId" db:"account_id"`
	Handle         string    `json:"handle" db:"handle"` // Unique channel username/handle
	DisplayName    string    `json:"displayName" db:"display_name"`
	Description    *string   `json:"description,omitempty" db:"description"`
	Support        *string   `json:"support,omitempty" db:"support"` // Support/donation text
	IsLocal        bool      `json:"isLocal" db:"is_local"`
	AtprotoDID     *string   `json:"atprotoDid,omitempty" db:"atproto_did"`        // ATProto DID for federation
	AtprotoPDSURL  *string   `json:"atprotoPdsUrl,omitempty" db:"atproto_pds_url"` // Optional PDS base URL for the DID
	AvatarFilename *string   `json:"avatarFilename,omitempty" db:"avatar_filename"`
	AvatarIPFSCID  *string   `json:"avatarIpfsCid,omitempty" db:"avatar_ipfs_cid"`
	BannerFilename *string   `json:"bannerFilename,omitempty" db:"banner_filename"`
	BannerIPFSCID  *string   `json:"bannerIpfsCid,omitempty" db:"banner_ipfs_cid"`
	FollowersCount int       `json:"followersCount" db:"followers_count"`
	FollowingCount int       `json:"followingCount" db:"following_count"`
	VideosCount    int       `json:"videosCount" db:"videos_count"`
	CreatedAt      time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt      time.Time `json:"updatedAt" db:"updated_at"`

	// Nested objects for API responses
	Account *User          `json:"account,omitempty" db:"-"` // The user account that owns this channel
	Avatar  *ChannelAvatar `json:"avatar,omitempty" db:"-"`  // Avatar details
	Banner  *ChannelBanner `json:"banner,omitempty" db:"-"`  // Banner details
}

// ChannelAvatar represents channel avatar information
type ChannelAvatar struct {
	Path      string    `json:"path,omitempty"`
	URL       string    `json:"url,omitempty"`
	IPFSCID   string    `json:"ipfsCid,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// ChannelBanner represents channel banner information
type ChannelBanner struct {
	Path      string    `json:"path,omitempty"`
	URL       string    `json:"url,omitempty"`
	IPFSCID   string    `json:"ipfsCid,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// ChannelListParams represents parameters for listing channels
type ChannelListParams struct {
	AccountID *uuid.UUID `json:"accountId,omitempty"`
	Search    string     `json:"search,omitempty"`
	Sort      string     `json:"sort,omitempty"` // "name", "-name", "createdAt", "-createdAt", "videosCount", "-videosCount"
	Page      int        `json:"page,omitempty"`
	PageSize  int        `json:"pageSize,omitempty"`
	IsLocal   *bool      `json:"isLocal,omitempty"`
}

// ChannelCreateRequest represents a request to create a new channel
type ChannelCreateRequest struct {
	Handle      string  `json:"handle" validate:"required,min=3,max=50,alphanum"`
	DisplayName string  `json:"displayName" validate:"required,min=1,max=255"`
	Description *string `json:"description,omitempty" validate:"omitempty,max=5000"`
	Support     *string `json:"support,omitempty" validate:"omitempty,max=1000"`
}

// ChannelUpdateRequest represents a request to update a channel
type ChannelUpdateRequest struct {
	DisplayName *string `json:"displayName,omitempty" validate:"omitempty,min=1,max=255"`
	Description *string `json:"description,omitempty" validate:"omitempty,max=5000"`
	Support     *string `json:"support,omitempty" validate:"omitempty,max=1000"`
}

// ChannelListResponse represents a paginated list of channels
type ChannelListResponse struct {
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	PageSize int       `json:"pageSize"`
	Data     []Channel `json:"data"`
}

// Validate validates a channel create request
func (r *ChannelCreateRequest) Validate() error {
	if r.Handle == "" {
		return ErrInvalidInput
	}
	if r.DisplayName == "" {
		return ErrInvalidInput
	}
	// Additional validation can be added here
	return nil
}

// Validate validates a channel update request
func (r *ChannelUpdateRequest) Validate() error {
	// At least one field should be provided for update
	if r.DisplayName == nil && r.Description == nil && r.Support == nil {
		return ErrInvalidInput
	}
	return nil
}
