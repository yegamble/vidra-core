package activitypub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"athena/internal/domain"
)

func (s *Service) HandleInboxActivity(ctx context.Context, activity map[string]interface{}, r *http.Request) error {
	actorID, ok := activity["actor"].(string)
	if !ok {
		return fmt.Errorf("missing or invalid actor")
	}

	remoteActor, err := s.FetchRemoteActor(ctx, actorID)
	if err != nil {
		return fmt.Errorf("failed to fetch remote actor: %w", err)
	}

	if err := s.sigVerifier.VerifyRequest(r, remoteActor.PublicKeyPem); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	activityID, ok := activity["id"].(string)
	if !ok {
		return fmt.Errorf("missing or invalid activity id")
	}

	received, err := s.repo.IsActivityReceived(ctx, activityID)
	if err != nil {
		return fmt.Errorf("failed to check duplicate: %w", err)
	}
	if received {
		return nil
	}

	if err := s.repo.MarkActivityReceived(ctx, activityID); err != nil {
		return fmt.Errorf("failed to mark received: %w", err)
	}

	activityType, ok := activity["type"].(string)
	if !ok {
		return fmt.Errorf("missing or invalid activity type")
	}

	switch activityType {
	case domain.ActivityTypeFollow:
		return s.handleFollow(ctx, activity, remoteActor)
	case domain.ActivityTypeUndo:
		return s.handleUndo(ctx, activity, remoteActor)
	case domain.ActivityTypeAccept:
		return s.handleAccept(ctx, activity, remoteActor)
	case domain.ActivityTypeReject:
		return s.handleReject(ctx, activity, remoteActor)
	case domain.ActivityTypeLike:
		return s.handleLike(ctx, activity, remoteActor)
	case domain.ActivityTypeAnnounce:
		return s.handleAnnounce(ctx, activity, remoteActor)
	case domain.ActivityTypeCreate:
		return s.handleCreate(ctx, activity, remoteActor)
	case domain.ActivityTypeUpdate:
		return s.handleUpdate(ctx, activity, remoteActor)
	case domain.ActivityTypeDelete:
		return s.handleDelete(ctx, activity, remoteActor)
	default:
		return nil
	}
}

func (s *Service) handleFollow(ctx context.Context, activity map[string]interface{}, remoteActor *domain.APRemoteActor) error {
	object, ok := activity["object"].(string)
	if !ok {
		return fmt.Errorf("invalid object in follow activity")
	}

	localUsername, err := s.extractUsernameFromURI(object)
	if err != nil {
		return fmt.Errorf("failed to extract username: %w", err)
	}

	localUser, err := s.userRepo.GetByUsername(ctx, localUsername)
	if err != nil || localUser == nil {
		return fmt.Errorf("local user not found")
	}

	follower := &domain.APFollower{
		ActorID:    localUser.ID,
		FollowerID: remoteActor.ActorURI,
		State:      "pending",
	}

	if s.cfg.ActivityPubAcceptFollowAutomatic {
		follower.State = "accepted"
	}

	if err := s.repo.UpsertFollower(ctx, follower); err != nil {
		return fmt.Errorf("failed to create follower: %w", err)
	}

	if s.cfg.ActivityPubAcceptFollowAutomatic {
		acceptActivity := map[string]interface{}{
			"@context": domain.ActivityStreamsContext,
			"type":     domain.ActivityTypeAccept,
			"actor":    s.buildActorID(localUsername),
			"object":   activity,
		}

		if err := s.DeliverActivity(ctx, localUser.ID, remoteActor.InboxURL, acceptActivity); err != nil {
			return fmt.Errorf("failed to deliver accept: %w", err)
		}
	}

	return nil
}

func (s *Service) handleUndo(ctx context.Context, activity map[string]interface{}, remoteActor *domain.APRemoteActor) error {
	object, ok := activity["object"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid object in undo activity")
	}

	objectType, ok := object["type"].(string)
	if !ok {
		return fmt.Errorf("missing type in undo object")
	}

	switch objectType {
	case domain.ActivityTypeFollow:
		objectTarget, ok := object["object"].(string)
		if !ok {
			return fmt.Errorf("invalid object in undo follow")
		}

		localUsername, err := s.extractUsernameFromURI(objectTarget)
		if err != nil {
			return err
		}

		localUser, err := s.userRepo.GetByUsername(ctx, localUsername)
		if err != nil || localUser == nil {
			return fmt.Errorf("local user not found")
		}

		return s.repo.DeleteFollower(ctx, localUser.ID, remoteActor.ActorURI)

	case domain.ActivityTypeLike:
		activityID, ok := object["id"].(string)
		if !ok {
			return fmt.Errorf("missing id in undo like")
		}
		return s.repo.DeleteVideoReaction(ctx, activityID)

	case domain.ActivityTypeAnnounce:
		activityID, ok := object["id"].(string)
		if !ok {
			return fmt.Errorf("missing id in undo announce")
		}
		return s.repo.DeleteVideoShare(ctx, activityID)

	default:
		return nil
	}
}

func (s *Service) handleAccept(ctx context.Context, activity map[string]interface{}, remoteActor *domain.APRemoteActor) error {
	object, ok := activity["object"].(map[string]interface{})
	if !ok {
		return nil
	}

	objectType, ok := object["type"].(string)
	if !ok || objectType != domain.ActivityTypeFollow {
		return nil
	}

	actorURI, ok := object["actor"].(string)
	if !ok {
		return fmt.Errorf("invalid actor in accept object")
	}

	localUsername, err := s.extractUsernameFromURI(actorURI)
	if err != nil {
		return err
	}

	localUser, err := s.userRepo.GetByUsername(ctx, localUsername)
	if err != nil || localUser == nil {
		return fmt.Errorf("local user not found")
	}

	follower, err := s.repo.GetFollower(ctx, remoteActor.ActorURI, localUser.ID)
	if err != nil {
		return fmt.Errorf("failed to get follower: %w", err)
	}

	if follower != nil {
		follower.State = "accepted"
		return s.repo.UpsertFollower(ctx, follower)
	}

	return nil
}

func (s *Service) handleReject(ctx context.Context, activity map[string]interface{}, remoteActor *domain.APRemoteActor) error {
	object, ok := activity["object"].(map[string]interface{})
	if !ok {
		return nil
	}

	objectType, ok := object["type"].(string)
	if !ok || objectType != domain.ActivityTypeFollow {
		return nil
	}

	actorURI, ok := object["actor"].(string)
	if !ok {
		return fmt.Errorf("invalid actor in reject object")
	}

	localUsername, err := s.extractUsernameFromURI(actorURI)
	if err != nil {
		return err
	}

	localUser, err := s.userRepo.GetByUsername(ctx, localUsername)
	if err != nil || localUser == nil {
		return fmt.Errorf("local user not found")
	}

	return s.repo.DeleteFollower(ctx, remoteActor.ActorURI, localUser.ID)
}

func (s *Service) handleLike(ctx context.Context, activity map[string]interface{}, remoteActor *domain.APRemoteActor) error {
	object, ok := activity["object"].(string)
	if !ok {
		return fmt.Errorf("invalid object in like activity")
	}

	videoID, err := s.extractVideoIDFromURI(object)
	if err != nil {
		return err
	}

	activityID, ok := activity["id"].(string)
	if !ok {
		return fmt.Errorf("missing activity id")
	}

	return s.repo.UpsertVideoReaction(ctx, videoID, remoteActor.ActorURI, "like", activityID)
}

func (s *Service) handleAnnounce(ctx context.Context, activity map[string]interface{}, remoteActor *domain.APRemoteActor) error {
	object, ok := activity["object"].(string)
	if !ok {
		return fmt.Errorf("invalid object in announce activity")
	}

	videoID, err := s.extractVideoIDFromURI(object)
	if err != nil {
		return err
	}

	activityID, ok := activity["id"].(string)
	if !ok {
		return fmt.Errorf("missing activity id")
	}

	return s.repo.UpsertVideoShare(ctx, videoID, remoteActor.ActorURI, activityID)
}

func (s *Service) handleCreate(ctx context.Context, activity map[string]interface{}, remoteActor *domain.APRemoteActor) error {
	activityJSON, err := json.Marshal(activity)
	if err != nil {
		return fmt.Errorf("failed to marshal activity: %w", err)
	}

	activityID, ok := activity["id"].(string)
	if !ok {
		return fmt.Errorf("missing activity id")
	}

	obj, ok := activity["object"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("missing or invalid object in Create activity")
	}

	objType, ok := obj["type"].(string)
	if !ok {
		return fmt.Errorf("missing object type")
	}

	objID, _ := obj["id"].(string)

	apActivity := &domain.APActivity{
		ActorID:      remoteActor.ActorURI,
		Type:         domain.ActivityTypeCreate,
		Published:    time.Now(),
		ActivityJSON: activityJSON,
		Local:        false,
		ObjectID:     &objID,
		ObjectType:   &objType,
	}

	if err := s.repo.StoreActivity(ctx, apActivity); err != nil {
		return fmt.Errorf("failed to store activity: %w", err)
	}

	switch objType {
	case "Video":
		return s.ingestRemoteVideo(ctx, obj, remoteActor, activityID)
	case "Note":
		return nil
	default:
		return nil
	}
}

func (s *Service) handleUpdate(ctx context.Context, activity map[string]interface{}, remoteActor *domain.APRemoteActor) error {
	return s.handleCreate(ctx, activity, remoteActor)
}

func (s *Service) handleDelete(ctx context.Context, activity map[string]interface{}, remoteActor *domain.APRemoteActor) error {
	object, ok := activity["object"].(string)
	if !ok {
		return fmt.Errorf("invalid object in delete activity")
	}

	activityJSON, err := json.Marshal(activity)
	if err != nil {
		return fmt.Errorf("failed to marshal activity: %w", err)
	}

	apActivity := &domain.APActivity{
		ActorID:      remoteActor.ActorURI,
		Type:         domain.ActivityTypeDelete,
		ObjectID:     &object,
		Published:    time.Now(),
		ActivityJSON: activityJSON,
		Local:        false,
	}

	return s.repo.StoreActivity(ctx, apActivity)
}
