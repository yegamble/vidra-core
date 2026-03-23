package activitypub

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"athena/internal/domain"
)

func (s *Service) BuildNoteObject(ctx context.Context, comment *domain.Comment) (*domain.NoteObject, error) {
	if comment == nil {
		return nil, fmt.Errorf("comment is nil")
	}

	user, err := s.userRepo.GetByID(ctx, comment.UserID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get comment author: %w", err)
	}

	video, err := s.videoRepo.GetByID(ctx, comment.VideoID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get video: %w", err)
	}

	noteID := fmt.Sprintf("%s/comments/%s", s.cfg.PublicBaseURL, comment.ID.String())

	attributedTo := s.buildActorID(user.Username)

	var inReplyTo string
	if comment.ParentID != nil {
		inReplyTo = fmt.Sprintf("%s/comments/%s", s.cfg.PublicBaseURL, comment.ParentID.String())
	} else {
		inReplyTo = fmt.Sprintf("%s/videos/%s", s.cfg.PublicBaseURL, comment.VideoID.String())
	}

	note := &domain.NoteObject{
		Type:         domain.ObjectTypeNote,
		ID:           noteID,
		Content:      comment.Body,
		Published:    &comment.CreatedAt,
		AttributedTo: attributedTo,
		InReplyTo:    inReplyTo,
	}

	if comment.EditedAt != nil {
		note.Updated = comment.EditedAt
	}

	switch video.Privacy {
	case domain.PrivacyPublic:
		note.To = []string{"https://www.w3.org/ns/activitystreams#Public"}
	case domain.PrivacyUnlisted:
		note.Cc = []string{"https://www.w3.org/ns/activitystreams#Public"}
	}

	if video.UserID != user.ID {
		videoOwner, err := s.userRepo.GetByID(ctx, video.UserID)
		if err == nil && videoOwner != nil {
			videoOwnerURI := s.buildActorID(videoOwner.Username)
			note.Cc = append(note.Cc, videoOwnerURI)
		}
	}

	return note, nil
}

func (s *Service) CreateCommentActivity(ctx context.Context, comment *domain.Comment) (*domain.Activity, error) {
	note, err := s.BuildNoteObject(ctx, comment)
	if err != nil {
		return nil, fmt.Errorf("failed to build note object: %w", err)
	}

	user, err := s.userRepo.GetByID(ctx, comment.UserID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get comment author: %w", err)
	}

	actorURI := s.buildActorID(user.Username)
	activityID := fmt.Sprintf("%s/activities/%s", s.cfg.PublicBaseURL, uuid.New().String())

	activity := &domain.Activity{
		Context:   []interface{}{domain.ActivityStreamsContext},
		Type:      domain.ActivityTypeCreate,
		ID:        activityID,
		Actor:     actorURI,
		Object:    note,
		Published: &comment.CreatedAt,
		To:        note.To,
		Cc:        note.Cc,
	}

	return activity, nil
}

func (s *Service) PublishComment(ctx context.Context, commentID string) error {
	comment, err := s.lookupComment(ctx, commentID)
	if err != nil {
		return err
	}

	if comment.Status == domain.CommentStatusDeleted {
		return fmt.Errorf("cannot publish deleted comment")
	}

	activity, err := s.CreateCommentActivity(ctx, comment)
	if err != nil {
		return fmt.Errorf("failed to create comment activity: %w", err)
	}

	noteID := activity.ID
	noteType := domain.ObjectTypeNote

	apActivity, err := s.storeCommentActivity(ctx, activity, comment.UserID.String(), domain.ActivityTypeCreate, &noteID, &noteType, comment.CreatedAt)
	if err != nil {
		return err
	}

	return s.enqueueCommentDeliveries(ctx, comment, apActivity, "publish")
}

func (s *Service) UpdateComment(ctx context.Context, commentID string) error {
	comment, err := s.lookupComment(ctx, commentID)
	if err != nil {
		return err
	}

	note, err := s.BuildNoteObject(ctx, comment)
	if err != nil {
		return fmt.Errorf("failed to build note object: %w", err)
	}

	user, err := s.userRepo.GetByID(ctx, comment.UserID.String())
	if err != nil {
		return fmt.Errorf("failed to get comment author: %w", err)
	}

	actorURI := s.buildActorID(user.Username)
	activityID := fmt.Sprintf("%s/activities/%s", s.cfg.PublicBaseURL, uuid.New().String())
	now := time.Now()

	activity := &domain.Activity{
		Context:   []interface{}{domain.ActivityStreamsContext},
		Type:      domain.ActivityTypeUpdate,
		ID:        activityID,
		Actor:     actorURI,
		Object:    note,
		Published: &now,
		To:        note.To,
		Cc:        note.Cc,
	}

	noteID := note.ID
	noteType := domain.ObjectTypeNote

	apActivity, err := s.storeCommentActivity(ctx, activity, user.ID, domain.ActivityTypeUpdate, &noteID, &noteType, now)
	if err != nil {
		return err
	}

	return s.enqueueCommentDeliveries(ctx, comment, apActivity, "update")
}

func (s *Service) DeleteComment(ctx context.Context, commentID string) error {
	comment, err := s.lookupComment(ctx, commentID)
	if err != nil {
		return err
	}

	user, err := s.userRepo.GetByID(ctx, comment.UserID.String())
	if err != nil {
		return fmt.Errorf("failed to get comment author: %w", err)
	}

	actorURI := s.buildActorID(user.Username)
	activityID := fmt.Sprintf("%s/activities/%s", s.cfg.PublicBaseURL, uuid.New().String())
	commentURI := fmt.Sprintf("%s/comments/%s", s.cfg.PublicBaseURL, comment.ID.String())
	now := time.Now()

	activity := &domain.Activity{
		Context:   []interface{}{domain.ActivityStreamsContext},
		Type:      domain.ActivityTypeDelete,
		ID:        activityID,
		Actor:     actorURI,
		Object:    commentURI,
		Published: &now,
	}

	apActivity, err := s.storeCommentActivity(ctx, activity, user.ID, domain.ActivityTypeDelete, &commentURI, nil, now)
	if err != nil {
		return err
	}

	return s.enqueueCommentDeliveries(ctx, comment, apActivity, "delete")
}

// lookupComment validates the comment repo, parses the ID, and fetches the comment.
func (s *Service) lookupComment(ctx context.Context, commentID string) (*domain.Comment, error) {
	if s.commentRepo == nil {
		return nil, fmt.Errorf("comment repository not configured")
	}

	commentUUID, err := uuid.Parse(commentID)
	if err != nil {
		return nil, fmt.Errorf("invalid comment ID: %w", err)
	}

	comment, err := s.commentRepo.GetByID(ctx, commentUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to get comment: %w", err)
	}

	return comment, nil
}

// storeCommentActivity marshals an activity and stores it as an APActivity.
func (s *Service) storeCommentActivity(
	ctx context.Context,
	activity *domain.Activity,
	actorID string,
	activityType string,
	objectID *string,
	objectType *string,
	published time.Time,
) (*domain.APActivity, error) {
	activityJSON, err := json.Marshal(activity)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal activity: %w", err)
	}

	apActivity := &domain.APActivity{
		ActorID:      actorID,
		Type:         activityType,
		ObjectID:     objectID,
		ObjectType:   objectType,
		Published:    published,
		ActivityJSON: activityJSON,
		Local:        true,
	}

	if err := s.repo.StoreActivity(ctx, apActivity); err != nil {
		return nil, fmt.Errorf("failed to store activity: %w", err)
	}

	return apActivity, nil
}

// enqueueCommentDeliveries fans out an activity to followers of the video owner.
func (s *Service) enqueueCommentDeliveries(ctx context.Context, comment *domain.Comment, apActivity *domain.APActivity, action string) error {
	video, err := s.videoRepo.GetByID(ctx, comment.VideoID.String())
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}

	followers, _, err := s.repo.GetFollowers(ctx, video.UserID, "accepted", 100, 0)
	if err != nil {
		return fmt.Errorf("failed to get followers: %w", err)
	}

	if len(followers) == 0 {
		return nil
	}

	followerURIs := make([]string, len(followers))
	for i, f := range followers {
		followerURIs[i] = f.FollowerID
	}

	remoteActors, err := s.repo.GetRemoteActors(ctx, followerURIs)
	if err != nil {
		return fmt.Errorf("failed to get remote actors: %w", err)
	}

	deliveries := make([]*domain.APDeliveryQueue, 0, len(remoteActors))
	for _, remoteActor := range remoteActors {
		deliveries = append(deliveries, &domain.APDeliveryQueue{
			ActivityID:  apActivity.ID,
			InboxURL:    remoteActor.InboxURL,
			ActorID:     comment.UserID.String(),
			Attempts:    0,
			MaxAttempts: 3,
			NextAttempt: time.Now(),
			Status:      "pending",
		})
	}

	if err := s.repo.BulkEnqueueDelivery(ctx, deliveries); err != nil {
		slog.Warn("failed to bulk enqueue "+action+" deliveries for comment", "id", comment.ID.String(), "error", err)
	}

	return nil
}
