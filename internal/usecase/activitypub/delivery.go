package activitypub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"athena/internal/activitypub"
	"athena/internal/domain"
)

func (s *Service) DeliverActivity(ctx context.Context, actorID, inboxURL string, activity interface{}) error {
	_, privateKey, err := s.repo.GetActorKeys(ctx, actorID)
	if err != nil {
		return fmt.Errorf("failed to get actor keys: %w", err)
	}

	activityJSON, err := json.Marshal(activity)
	if err != nil {
		return fmt.Errorf("failed to marshal activity: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", inboxURL, strings.NewReader(string(activityJSON)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/activity+json")
	req.Header.Set("User-Agent", "Athena/1.0")
	req.Header.Set("Host", req.URL.Host)

	user, err := s.userRepo.GetByID(ctx, actorID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	keyID := s.buildActorID(user.Username) + "#main-key"

	if err := activitypub.SignRequest(req, privateKey, keyID); err != nil {
		return fmt.Errorf("failed to sign request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to deliver activity: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delivery failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (s *Service) enqueueFollowerDeliveries(ctx context.Context, userID string, activity *domain.Activity, resourceDesc string) error {
	followers, _, err := s.repo.GetFollowers(ctx, userID, "accepted", 1000, 0)
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
			inboxURL := remoteActor.InboxURL
			if remoteActor.SharedInbox != nil && *remoteActor.SharedInbox != "" {
				inboxURL = *remoteActor.SharedInbox
			}

			deliveries = append(deliveries, &domain.APDeliveryQueue{
				ActivityID:  activity.ID,
				ActorID:     activity.Actor,
				InboxURL:    inboxURL,
				Attempts:    0,
				MaxAttempts: 10,
				NextAttempt: time.Now(),
				Status:      "pending",
			})
		}

		if err := s.repo.BulkEnqueueDelivery(ctx, deliveries); err != nil {
			slog.Warn("failed to bulk enqueue deliveries", "resource", resourceDesc, "error", err)
		}
	}

	return nil
}

func (s *Service) storeOutboxActivity(ctx context.Context, activity *domain.Activity) error {
	apActivity := &domain.APActivity{
		ID:           activity.ID,
		ActorID:      activity.Actor,
		Type:         activity.Type,
		Published:    *activity.Published,
		ActivityJSON: nil,
		Local:        true,
	}

	if err := s.repo.StoreActivity(ctx, apActivity); err != nil {
		return fmt.Errorf("failed to store activity: %w", err)
	}

	return nil
}

func (s *Service) getOrCreateActorKeys(ctx context.Context, actorID string) (publicKey, privateKey string, err error) {
	publicKey, privateKey, err = s.repo.GetActorKeys(ctx, actorID)
	if err == nil {
		return publicKey, privateKey, nil
	}

	publicKey, privateKey, err = activitypub.GenerateKeyPair()
	if err != nil {
		return "", "", fmt.Errorf("failed to generate key pair: %w", err)
	}

	if err := s.repo.StoreActorKeys(ctx, actorID, publicKey, privateKey); err != nil {
		return "", "", fmt.Errorf("failed to store keys: %w", err)
	}

	return publicKey, privateKey, nil
}
