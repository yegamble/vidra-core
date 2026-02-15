package activitypub

import (
	"context"
	"encoding/json"
	"fmt"

	"athena/internal/domain"
)

func (s *Service) GetOutbox(ctx context.Context, username string, page int, limit int) (*domain.OrderedCollectionPage, error) {
	user, err := s.userRepo.GetByUsername(ctx, username)
	if err != nil || user == nil {
		return nil, fmt.Errorf("user not found")
	}

	offset := page * limit
	activities, total, err := s.repo.GetActivitiesByActor(ctx, user.ID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get activities: %w", err)
	}

	actorID := s.buildActorID(username)
	outboxID := actorID + "/outbox"

	items := make([]interface{}, len(activities))
	for i, activity := range activities {
		var activityObj interface{}
		if err := json.Unmarshal(activity.ActivityJSON, &activityObj); err != nil {
			continue
		}
		items[i] = activityObj
	}

	collectionPage := &domain.OrderedCollectionPage{
		Context:      domain.ActivityStreamsContext,
		Type:         domain.ObjectTypeOrderedCollectionPage,
		ID:           fmt.Sprintf("%s?page=%d", outboxID, page),
		TotalItems:   total,
		PartOf:       outboxID,
		OrderedItems: items,
	}

	if page > 0 {
		collectionPage.Prev = fmt.Sprintf("%s?page=%d", outboxID, page-1)
	}

	if offset+limit < total {
		collectionPage.Next = fmt.Sprintf("%s?page=%d", outboxID, page+1)
	}

	return collectionPage, nil
}

func (s *Service) buildFollowCollectionPage(
	username, collectionType string,
	page, limit, total int,
	items []interface{},
) *domain.OrderedCollectionPage {
	actorID := s.buildActorID(username)
	collectionID := actorID + "/" + collectionType
	offset := page * limit

	collectionPage := &domain.OrderedCollectionPage{
		Context:      domain.ActivityStreamsContext,
		Type:         domain.ObjectTypeOrderedCollectionPage,
		ID:           fmt.Sprintf("%s?page=%d", collectionID, page),
		TotalItems:   total,
		PartOf:       collectionID,
		OrderedItems: items,
	}

	if page > 0 {
		collectionPage.Prev = fmt.Sprintf("%s?page=%d", collectionID, page-1)
	}

	if offset+limit < total {
		collectionPage.Next = fmt.Sprintf("%s?page=%d", collectionID, page+1)
	}

	return collectionPage
}

func (s *Service) GetFollowers(ctx context.Context, username string, page int, limit int) (*domain.OrderedCollectionPage, error) {
	user, err := s.userRepo.GetByUsername(ctx, username)
	if err != nil || user == nil {
		return nil, fmt.Errorf("user not found")
	}

	offset := page * limit
	followers, total, err := s.repo.GetFollowers(ctx, user.ID, "accepted", limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get followers: %w", err)
	}

	items := make([]interface{}, len(followers))
	for i, follower := range followers {
		items[i] = follower.FollowerID
	}

	return s.buildFollowCollectionPage(username, "followers", page, limit, total, items), nil
}

func (s *Service) GetFollowing(ctx context.Context, username string, page int, limit int) (*domain.OrderedCollectionPage, error) {
	user, err := s.userRepo.GetByUsername(ctx, username)
	if err != nil || user == nil {
		return nil, fmt.Errorf("user not found")
	}

	offset := page * limit
	following, total, err := s.repo.GetFollowing(ctx, user.ID, "accepted", limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get following: %w", err)
	}

	items := make([]interface{}, len(following))
	for i, follow := range following {
		items[i] = follow.ActorID
	}

	return s.buildFollowCollectionPage(username, "following", page, limit, total, items), nil
}

func (s *Service) GetOutboxCount(ctx context.Context, username string) (int, error) {
	user, err := s.userRepo.GetByUsername(ctx, username)
	if err != nil || user == nil {
		return 0, fmt.Errorf("user not found")
	}

	_, total, err := s.repo.GetActivitiesByActor(ctx, user.ID, 0, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to get activities count: %w", err)
	}

	return total, nil
}

func (s *Service) GetFollowersCount(ctx context.Context, username string) (int, error) {
	user, err := s.userRepo.GetByUsername(ctx, username)
	if err != nil || user == nil {
		return 0, fmt.Errorf("user not found")
	}

	_, total, err := s.repo.GetFollowers(ctx, user.ID, "accepted", 0, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to get followers count: %w", err)
	}

	return total, nil
}

func (s *Service) GetFollowingCount(ctx context.Context, username string) (int, error) {
	user, err := s.userRepo.GetByUsername(ctx, username)
	if err != nil || user == nil {
		return 0, fmt.Errorf("user not found")
	}

	_, total, err := s.repo.GetFollowing(ctx, user.ID, "accepted", 0, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to get following count: %w", err)
	}

	return total, nil
}
