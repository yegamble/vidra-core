package domain

import (
	"encoding/json"
	"time"
)

// SocialInteractionType represents types of social interactions via ATProto
type SocialInteractionType string

const (
	InteractionFollow  SocialInteractionType = "follow"
	InteractionLike    SocialInteractionType = "like"
	InteractionComment SocialInteractionType = "comment"
	InteractionRepost  SocialInteractionType = "repost"
)

// Follow represents an ATProto follow relationship
type Follow struct {
	ID              string          `json:"id" db:"id"`
	FollowerDID     string          `json:"follower_did" db:"follower_did"`   // ATProto DID of follower
	FollowingDID    string          `json:"following_did" db:"following_did"` // ATProto DID of followed account
	FollowerHandle  *string         `json:"follower_handle,omitempty" db:"follower_handle"`
	FollowingHandle *string         `json:"following_handle,omitempty" db:"following_handle"`
	URI             string          `json:"uri" db:"uri"`           // at:// URI of follow record
	CID             *string         `json:"cid,omitempty" db:"cid"` // CID of the follow record
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
	RevokedAt       *time.Time      `json:"revoked_at,omitempty" db:"revoked_at"` // When unfollowed
	Raw             json.RawMessage `json:"raw,omitempty" db:"raw"`               // Raw ATProto record
}

// Like represents an ATProto like on a post/video
type Like struct {
	ID          string          `json:"id" db:"id"`
	ActorDID    string          `json:"actor_did" db:"actor_did"` // Who liked
	ActorHandle *string         `json:"actor_handle,omitempty" db:"actor_handle"`
	SubjectURI  string          `json:"subject_uri" db:"subject_uri"` // What was liked (at:// URI)
	SubjectCID  *string         `json:"subject_cid,omitempty" db:"subject_cid"`
	URI         string          `json:"uri" db:"uri"` // at:// URI of like record
	CID         *string         `json:"cid,omitempty" db:"cid"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	VideoID     *string         `json:"video_id,omitempty" db:"video_id"` // Local video if applicable
	PostID      *string         `json:"post_id,omitempty" db:"post_id"`   // Federated post if applicable
	Raw         json.RawMessage `json:"raw,omitempty" db:"raw"`
}

// SocialComment represents an ATProto reply/comment
type SocialComment struct {
	ID          string          `json:"id" db:"id"`
	ActorDID    string          `json:"actor_did" db:"actor_did"`
	ActorHandle *string         `json:"actor_handle,omitempty" db:"actor_handle"`
	DisplayName *string         `json:"display_name,omitempty" db:"display_name"`
	URI         string          `json:"uri" db:"uri"` // at:// URI of comment record
	CID         *string         `json:"cid,omitempty" db:"cid"`
	Text        string          `json:"text" db:"text"`
	ParentURI   *string         `json:"parent_uri,omitempty" db:"parent_uri"` // Reply parent
	ParentCID   *string         `json:"parent_cid,omitempty" db:"parent_cid"`
	RootURI     string          `json:"root_uri" db:"root_uri"` // Thread root
	RootCID     *string         `json:"root_cid,omitempty" db:"root_cid"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	IndexedAt   time.Time       `json:"indexed_at" db:"indexed_at"`
	VideoID     *string         `json:"video_id,omitempty" db:"video_id"` // Local video if applicable
	PostID      *string         `json:"post_id,omitempty" db:"post_id"`   // Federated post if applicable
	Labels      json.RawMessage `json:"labels,omitempty" db:"labels"`     // Moderation labels
	Blocked     bool            `json:"blocked" db:"blocked"`             // Hidden by moderation
	Raw         json.RawMessage `json:"raw,omitempty" db:"raw"`
}

// ModerationLabel represents ATProto moderation labels
type ModerationLabel struct {
	ID        string          `json:"id" db:"id"`
	ActorDID  string          `json:"actor_did" db:"actor_did"`   // Who is labeled
	LabelType string          `json:"label_type" db:"label_type"` // e.g., "spam", "impersonation"
	Reason    *string         `json:"reason,omitempty" db:"reason"`
	AppliedBy string          `json:"applied_by" db:"applied_by"` // DID of labeler
	URI       *string         `json:"uri,omitempty" db:"uri"`     // Subject URI if content-specific
	CreatedAt time.Time       `json:"created_at" db:"created_at"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty" db:"expires_at"`
	Raw       json.RawMessage `json:"raw,omitempty" db:"raw"`
}

// SocialStats aggregates social interaction counts
type SocialStats struct {
	Follows   int64 `json:"follows" db:"follows"`
	Followers int64 `json:"followers" db:"followers"`
	Likes     int64 `json:"likes" db:"likes"`
	Comments  int64 `json:"comments" db:"comments"`
	Reposts   int64 `json:"reposts" db:"reposts"`
}

// ATProtoActor represents an actor (user) in the ATProto network
type ATProtoActor struct {
	DID         string          `json:"did" db:"did"`
	Handle      string          `json:"handle" db:"handle"`
	DisplayName *string         `json:"display_name,omitempty" db:"display_name"`
	Bio         *string         `json:"bio,omitempty" db:"bio"`
	AvatarURL   *string         `json:"avatar_url,omitempty" db:"avatar_url"`
	BannerURL   *string         `json:"banner_url,omitempty" db:"banner_url"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at" db:"updated_at"`
	IndexedAt   time.Time       `json:"indexed_at" db:"indexed_at"`
	Labels      json.RawMessage `json:"labels,omitempty" db:"labels"`
	LocalUserID *string         `json:"local_user_id,omitempty" db:"local_user_id"` // Link to local user if exists
}
