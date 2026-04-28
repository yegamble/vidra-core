package inner_circle

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
)

// ErrPostBodyEmpty is returned when a create/update tries to persist an empty body.
var ErrPostBodyEmpty = errors.New("inner_circle: post body must be non-empty")

// ErrPostBodyTooLong is returned when body exceeds the 4 KB limit.
var ErrPostBodyTooLong = errors.New("inner_circle: post body must be <= 4096 chars")

// ErrAttachmentsRejected is returned when callers attempt to send an
// `attachments` field. Phase 9 is text-only; the field is reserved for 9b.
var ErrAttachmentsRejected = errors.New("inner_circle: image attachments are deferred to Phase 9b — pass body only")

// PostRepo is the subset of the post repository the service uses.
type PostRepo interface {
	Create(ctx context.Context, channelID uuid.UUID, body string, tierID *string) (*domain.ChannelPost, error)
	Update(ctx context.Context, postID, channelID uuid.UUID, body *string, tierID *string, clearTier bool) (*domain.ChannelPost, error)
	Get(ctx context.Context, postID, channelID uuid.UUID) (*domain.ChannelPost, error)
	Delete(ctx context.Context, postID, channelID uuid.UUID) error
	List(ctx context.Context, channelID uuid.UUID, cursor *uuid.UUID, limit int) ([]domain.ChannelPost, error)
}

// PostMembershipLookup is the subset of the membership repository the post
// service uses to resolve the caller's tier when computing post visibility.
type PostMembershipLookup interface {
	GetActiveTier(ctx context.Context, userID, channelID uuid.UUID) (string, error)
}

// PostService coordinates channel-post CRUD with tier-based read gating.
type PostService struct {
	posts       PostRepo
	memberships PostMembershipLookup
	channels    ChannelLookup
}

// NewPostService wires the service.
func NewPostService(posts PostRepo, memberships PostMembershipLookup, channels ChannelLookup) *PostService {
	return &PostService{posts: posts, memberships: memberships, channels: channels}
}

const maxPostBody = 4096

// CreateInput captures the fields the caller may supply on POST.
type CreateInput struct {
	Body              string
	TierID            *string
	HasAttachmentsRaw bool // set by handler when the JSON contained an `attachments` field
}

// Create validates inputs and persists a new post owned by the channel. Caller
// must be the channel's account_id.
func (s *PostService) Create(ctx context.Context, channelID, callerUserID uuid.UUID, in CreateInput) (*domain.ChannelPost, error) {
	if in.HasAttachmentsRaw {
		return nil, ErrAttachmentsRejected
	}
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return nil, ErrPostBodyEmpty
	}
	if len(body) > maxPostBody {
		return nil, ErrPostBodyTooLong
	}
	if in.TierID != nil && *in.TierID != "" && !ValidTierID(*in.TierID) {
		return nil, fmt.Errorf("inner_circle: invalid tier_id %q", *in.TierID)
	}
	channel, err := s.channels.GetByID(ctx, channelID)
	if err != nil {
		return nil, ErrChannelNotFound
	}
	if channel.AccountID != callerUserID {
		return nil, ErrNotChannelOwner
	}

	// Treat empty-string tier as nil (public post).
	var tier *string
	if in.TierID != nil && *in.TierID != "" {
		t := *in.TierID
		tier = &t
	}
	return s.posts.Create(ctx, channelID, body, tier)
}

// UpdateInput captures partial-update fields for PATCH.
type UpdateInput struct {
	Body              *string
	TierID            *string // nil = no change; empty string = clear gate
	HasAttachmentsRaw bool
}

// Update applies a partial update to a post owned by callerUserID's channel.
func (s *PostService) Update(ctx context.Context, postID, channelID, callerUserID uuid.UUID, in UpdateInput) (*domain.ChannelPost, error) {
	if in.HasAttachmentsRaw {
		return nil, ErrAttachmentsRejected
	}
	channel, err := s.channels.GetByID(ctx, channelID)
	if err != nil {
		return nil, ErrChannelNotFound
	}
	if channel.AccountID != callerUserID {
		return nil, ErrNotChannelOwner
	}
	if in.Body != nil {
		body := strings.TrimSpace(*in.Body)
		if body == "" {
			return nil, ErrPostBodyEmpty
		}
		if len(body) > maxPostBody {
			return nil, ErrPostBodyTooLong
		}
		in.Body = &body
	}
	clearTier := false
	if in.TierID != nil && *in.TierID == "" {
		clearTier = true
		in.TierID = nil
	}
	if in.TierID != nil && !ValidTierID(*in.TierID) {
		return nil, fmt.Errorf("inner_circle: invalid tier_id %q", *in.TierID)
	}
	return s.posts.Update(ctx, postID, channelID, in.Body, in.TierID, clearTier)
}

// Delete hard-deletes a post owned by the caller's channel.
func (s *PostService) Delete(ctx context.Context, postID, channelID, callerUserID uuid.UUID) error {
	channel, err := s.channels.GetByID(ctx, channelID)
	if err != nil {
		return ErrChannelNotFound
	}
	if channel.AccountID != callerUserID {
		return ErrNotChannelOwner
	}
	return s.posts.Delete(ctx, postID, channelID)
}

// PostView is the projected shape returned to the API. When Locked is true,
// Body is empty — non-members do not see gated content.
type PostView struct {
	Post   *domain.ChannelPost
	Locked bool
}

// List returns posts paginated and gated by the caller's tier on the channel.
// callerUserID may be uuid.Nil for anonymous viewers.
func (s *PostService) List(ctx context.Context, channelID, callerUserID uuid.UUID, cursor *uuid.UUID, limit int) ([]PostView, error) {
	if _, err := s.channels.GetByID(ctx, channelID); err != nil {
		return nil, ErrChannelNotFound
	}
	posts, err := s.posts.List(ctx, channelID, cursor, limit)
	if err != nil {
		return nil, err
	}

	// Resolve caller tier once per channel rather than per post.
	memberTier := ""
	if callerUserID != uuid.Nil && s.memberships != nil {
		tier, err := s.memberships.GetActiveTier(ctx, callerUserID, channelID)
		if err == nil {
			memberTier = tier
		}
	}
	// Channel owners always see all posts.
	channelOwner := false
	if ch, err := s.channels.GetByID(ctx, channelID); err == nil && ch.AccountID == callerUserID {
		channelOwner = true
	}

	out := make([]PostView, 0, len(posts))
	for i := range posts {
		p := &posts[i]
		required := ""
		if p.TierID != nil {
			required = *p.TierID
		}
		visible := channelOwner || HasAccess(memberTier, required)
		view := PostView{Post: p, Locked: !visible}
		if !visible {
			lockedCopy := *p
			lockedCopy.Body = "" // non-members never see body
			view.Post = &lockedCopy
		}
		out = append(out, view)
	}
	return out, nil
}
