package activitypub

import (
	"context"
	"encoding/json"
	"fmt"
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
	if s.commentRepo == nil {
		return fmt.Errorf("comment repository not configured")
	}

	commentUUID, err := uuid.Parse(commentID)
	if err != nil {
		return fmt.Errorf("invalid comment ID: %w", err)
	}

	comment, err := s.commentRepo.GetByID(ctx, commentUUID)
	if err != nil {
		return fmt.Errorf("failed to get comment: %w", err)
	}

	if comment.Status == domain.CommentStatusDeleted {
		return fmt.Errorf("cannot publish deleted comment")
	}

	activity, err := s.CreateCommentActivity(ctx, comment)
	if err != nil {
		return fmt.Errorf("failed to create comment activity: %w", err)
	}

	activityJSON, err := json.Marshal(activity)
	if err != nil {
		return fmt.Errorf("failed to marshal activity: %w", err)
	}

	noteID := activity.ID
	noteType := domain.ObjectTypeNote

	apActivity := &domain.APActivity{
		ActorID:      comment.UserID.String(),
		Type:         domain.ActivityTypeCreate,
		ObjectID:     &noteID,
		ObjectType:   &noteType,
		Published:    comment.CreatedAt,
		ActivityJSON: activityJSON,
		Local:        true,
	}

	if err := s.repo.StoreActivity(ctx, apActivity); err != nil {
		return fmt.Errorf("failed to store activity: %w", err)
	}

	video, err := s.videoRepo.GetByID(ctx, comment.VideoID.String())
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}

	followers, _, err := s.repo.GetFollowers(ctx, video.UserID, "accepted", 100, 0)
	if err != nil {
		return fmt.Errorf("failed to get followers: %w", err)
	}

	if len(followers) > 0 {
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
			fmt.Printf("failed to bulk enqueue deliveries for comment %s: %v\n", commentID, err)
		}
	}

	return nil
}

func (s *Service) UpdateComment(ctx context.Context, commentID string) error {
	if s.commentRepo == nil {
		return fmt.Errorf("comment repository not configured")
	}

	commentUUID, err := uuid.Parse(commentID)
	if err != nil {
		return fmt.Errorf("invalid comment ID: %w", err)
	}

	comment, err := s.commentRepo.GetByID(ctx, commentUUID)
	if err != nil {
		return fmt.Errorf("failed to get comment: %w", err)
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

	activityJSON, err := json.Marshal(activity)
	if err != nil {
		return fmt.Errorf("failed to marshal activity: %w", err)
	}

	noteID := note.ID
	noteType := domain.ObjectTypeNote

	apActivity := &domain.APActivity{
		ActorID:      user.ID,
		Type:         domain.ActivityTypeUpdate,
		ObjectID:     &noteID,
		ObjectType:   &noteType,
		Published:    now,
		ActivityJSON: activityJSON,
		Local:        true,
	}

	if err := s.repo.StoreActivity(ctx, apActivity); err != nil {
		return fmt.Errorf("failed to store activity: %w", err)
	}

	video, err := s.videoRepo.GetByID(ctx, comment.VideoID.String())
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}

	followers, _, err := s.repo.GetFollowers(ctx, video.UserID, "accepted", 100, 0)
	if err != nil {
		return fmt.Errorf("failed to get followers: %w", err)
	}

	if len(followers) > 0 {
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
			fmt.Printf("failed to bulk enqueue update deliveries for comment %s: %v\n", commentID, err)
		}
	}

	return nil
}

func (s *Service) DeleteComment(ctx context.Context, commentID string) error {
	if s.commentRepo == nil {
		return fmt.Errorf("comment repository not configured")
	}

	commentUUID, err := uuid.Parse(commentID)
	if err != nil {
		return fmt.Errorf("invalid comment ID: %w", err)
	}

	comment, err := s.commentRepo.GetByID(ctx, commentUUID)
	if err != nil {
		return fmt.Errorf("failed to get comment: %w", err)
	}

	user, err := s.userRepo.GetByID(ctx, comment.UserID.String())
	if err != nil {
		return fmt.Errorf("failed to get comment author: %w", err)
	}

	video, err := s.videoRepo.GetByID(ctx, comment.VideoID.String())
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
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

	activityJSON, err := json.Marshal(activity)
	if err != nil {
		return fmt.Errorf("failed to marshal activity: %w", err)
	}

	apActivity := &domain.APActivity{
		ActorID:      user.ID,
		Type:         domain.ActivityTypeDelete,
		ObjectID:     &commentURI,
		Published:    now,
		ActivityJSON: activityJSON,
		Local:        true,
	}

	if err := s.repo.StoreActivity(ctx, apActivity); err != nil {
		return fmt.Errorf("failed to store activity: %w", err)
	}

	followers, _, err := s.repo.GetFollowers(ctx, video.UserID, "accepted", 100, 0)
	if err != nil {
		return fmt.Errorf("failed to get followers: %w", err)
	}

	if len(followers) > 0 {
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
			fmt.Printf("failed to bulk enqueue delete deliveries for comment %s: %v\n", commentID, err)
		}
	}

	return nil
}
