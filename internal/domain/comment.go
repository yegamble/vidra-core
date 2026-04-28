package domain

import (
	"time"

	"github.com/google/uuid"
)

type CommentStatus string

const (
	CommentStatusActive  CommentStatus = "active"
	CommentStatusDeleted CommentStatus = "deleted"
	CommentStatusFlagged CommentStatus = "flagged"
	CommentStatusHidden  CommentStatus = "hidden"
)

type Comment struct {
	ID            uuid.UUID     `json:"id" db:"id"`
	VideoID       uuid.UUID     `json:"video_id" db:"video_id"`
	UserID        uuid.UUID     `json:"user_id" db:"user_id"`
	ParentID      *uuid.UUID    `json:"parent_id,omitempty" db:"parent_id"`
	Body          string        `json:"body" db:"body"`
	Status        CommentStatus `json:"status" db:"status"`
	FlagCount     int           `json:"flag_count" db:"flag_count"`
	HeldForReview bool          `json:"held_for_review" db:"held_for_review"`
	Approved      bool          `json:"approved" db:"approved"`
	EditedAt      *time.Time    `json:"edited_at,omitempty" db:"edited_at"`
	CreatedAt     time.Time     `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at" db:"updated_at"`
	User          *User         `json:"user,omitempty"`
	Replies       []*Comment    `json:"replies,omitempty"`
	// Phase 9: highest active Inner Circle tier the commenter holds for the
	// video's channel. Resolved at read time via LEFT JOIN — null when the
	// commenter is not a member or membership has expired/cancelled.
	InnerCircleTier *string `json:"inner_circle_tier,omitempty" db:"inner_circle_tier"`
}

type CommentFlag struct {
	ID        uuid.UUID         `json:"id" db:"id"`
	CommentID uuid.UUID         `json:"comment_id" db:"comment_id"`
	UserID    uuid.UUID         `json:"user_id" db:"user_id"`
	Reason    CommentFlagReason `json:"reason" db:"reason"`
	Details   *string           `json:"details,omitempty" db:"details"`
	CreatedAt time.Time         `json:"created_at" db:"created_at"`
}

type CommentFlagReason string

const (
	FlagReasonSpam           CommentFlagReason = "spam"
	FlagReasonHarassment     CommentFlagReason = "harassment"
	FlagReasonHateSpeech     CommentFlagReason = "hate_speech"
	FlagReasonInappropriate  CommentFlagReason = "inappropriate"
	FlagReasonMisinformation CommentFlagReason = "misinformation"
	FlagReasonOther          CommentFlagReason = "other"
)

type CreateCommentRequest struct {
	VideoID  uuid.UUID  `json:"video_id"`
	ParentID *uuid.UUID `json:"parent_id,omitempty"`
	Body     string     `json:"body" validate:"required,min=1,max=10000"`
}

type UpdateCommentRequest struct {
	Body string `json:"body" validate:"required,min=1,max=10000"`
}

type FlagCommentRequest struct {
	Reason  CommentFlagReason `json:"reason" validate:"required,oneof=spam harassment hate_speech inappropriate misinformation other"`
	Details *string           `json:"details,omitempty" validate:"omitempty,max=500"`
}

type CommentListOptions struct {
	VideoID  uuid.UUID
	ParentID *uuid.UUID
	UserID   *uuid.UUID
	Status   *CommentStatus
	Limit    int
	Offset   int
	OrderBy  string
}

type CommentWithUser struct {
	Comment
	Username string  `json:"username" db:"username"`
	Avatar   *string `json:"avatar,omitempty" db:"avatar"`
}

// AdminCommentListOptions provides filtering for instance-wide comment listing.
type AdminCommentListOptions struct {
	VideoID       *uuid.UUID
	AccountName   *string
	Status        *CommentStatus
	HeldForReview *bool
	SearchText    *string
	Limit         int
	Offset        int
	OrderBy       string
}

// BulkRemoveCommentsRequest represents a request to bulk-remove comments by account.
type BulkRemoveCommentsRequest struct {
	AccountName string `json:"accountName" validate:"required,min=1"`
	Scope       string `json:"scope" validate:"required,oneof=instance my-videos"`
}
