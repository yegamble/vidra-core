package activitypub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"athena/internal/activitypub"
	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/port"
)

// Service handles ActivityPub federation logic
type Service struct {
	repo        port.ActivityPubRepository
	userRepo    port.UserRepository
	videoRepo   port.VideoRepository
	cfg         *config.Config
	httpClient  *http.Client
	sigVerifier *activitypub.HTTPSignatureVerifier
}

// NewService creates a new ActivityPub service
func NewService(
	repo port.ActivityPubRepository,
	userRepo port.UserRepository,
	videoRepo port.VideoRepository,
	cfg *config.Config,
) *Service {
	return &Service{
		repo:        repo,
		userRepo:    userRepo,
		videoRepo:   videoRepo,
		cfg:         cfg,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		sigVerifier: activitypub.NewHTTPSignatureVerifier(),
	}
}

// Actor Management

// GetLocalActor builds an Actor object for a local user
func (s *Service) GetLocalActor(ctx context.Context, username string) (*domain.Actor, error) {
	user, err := s.userRepo.GetByUsername(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("user not found")
	}

	actorID := s.buildActorID(username)

	// Get or create key pair
	publicKey, _, err := s.getOrCreateActorKeys(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get actor keys: %w", err)
	}

	actor := &domain.Actor{
		Context:           []interface{}{domain.ActivityStreamsContext, domain.SecurityContext},
		Type:              domain.ObjectTypePerson,
		ID:                actorID,
		Following:         actorID + "/following",
		Followers:         actorID + "/followers",
		Inbox:             actorID + "/inbox",
		Outbox:            actorID + "/outbox",
		PreferredUsername: username,
		Name:              user.Username,
		URL:               s.cfg.PublicBaseURL + "/users/" + username,
		PublicKey: &domain.PublicKey{
			ID:           actorID + "#main-key",
			Owner:        actorID,
			PublicKeyPem: publicKey,
		},
		Endpoints: &domain.Endpoints{
			SharedInbox: s.cfg.PublicBaseURL + "/inbox",
		},
	}

	if !user.CreatedAt.IsZero() {
		actor.Published = &user.CreatedAt
	}

	return actor, nil
}

// FetchRemoteActor fetches a remote actor and caches it
func (s *Service) FetchRemoteActor(ctx context.Context, actorURI string) (*domain.APRemoteActor, error) {
	// Check cache first
	cached, err := s.repo.GetRemoteActor(ctx, actorURI)
	if err != nil {
		return nil, fmt.Errorf("failed to check cache: %w", err)
	}

	// If cached and recently fetched, return it
	if cached != nil && cached.LastFetchedAt != nil {
		if time.Since(*cached.LastFetchedAt) < 24*time.Hour {
			return cached, nil
		}
	}

	// Fetch from remote
	req, err := http.NewRequestWithContext(ctx, "GET", actorURI, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/activity+json, application/ld+json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch actor: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var actor domain.Actor
	if err := json.Unmarshal(body, &actor); err != nil {
		return nil, fmt.Errorf("failed to parse actor: %w", err)
	}

	// Extract domain from URI
	u, err := url.Parse(actorURI)
	if err != nil {
		return nil, fmt.Errorf("failed to parse actor URI: %w", err)
	}

	// Build remote actor
	remoteActor := &domain.APRemoteActor{
		ActorURI:     actorURI,
		Type:         actor.Type,
		Username:     actor.PreferredUsername,
		Domain:       u.Host,
		DisplayName:  &actor.Name,
		Summary:      &actor.Summary,
		InboxURL:     actor.Inbox,
		OutboxURL:    &actor.Outbox,
		FollowersURL: &actor.Followers,
		FollowingURL: &actor.Following,
	}

	if actor.PublicKey != nil {
		remoteActor.PublicKeyID = actor.PublicKey.ID
		remoteActor.PublicKeyPem = actor.PublicKey.PublicKeyPem
	}

	if actor.Endpoints != nil && actor.Endpoints.SharedInbox != "" {
		remoteActor.SharedInbox = &actor.Endpoints.SharedInbox
	}

	if actor.Icon != nil {
		remoteActor.IconURL = &actor.Icon.URL
	}

	if actor.Image != nil {
		remoteActor.ImageURL = &actor.Image.URL
	}

	// Cache the actor
	if err := s.repo.UpsertRemoteActor(ctx, remoteActor); err != nil {
		return nil, fmt.Errorf("failed to cache actor: %w", err)
	}

	return remoteActor, nil
}

// Activity Handling

// HandleInboxActivity processes an incoming activity
func (s *Service) HandleInboxActivity(ctx context.Context, activity map[string]interface{}, r *http.Request) error {
	// Verify HTTP signature
	actorID, ok := activity["actor"].(string)
	if !ok {
		return fmt.Errorf("missing or invalid actor")
	}

	// Fetch remote actor to get public key
	remoteActor, err := s.FetchRemoteActor(ctx, actorID)
	if err != nil {
		return fmt.Errorf("failed to fetch remote actor: %w", err)
	}

	// Verify signature
	if err := s.sigVerifier.VerifyRequest(r, remoteActor.PublicKeyPem); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	// Check for duplicate
	activityID, ok := activity["id"].(string)
	if !ok {
		return fmt.Errorf("missing or invalid activity id")
	}

	received, err := s.repo.IsActivityReceived(ctx, activityID)
	if err != nil {
		return fmt.Errorf("failed to check duplicate: %w", err)
	}
	if received {
		return nil // Already processed
	}

	// Mark as received
	if err := s.repo.MarkActivityReceived(ctx, activityID); err != nil {
		return fmt.Errorf("failed to mark received: %w", err)
	}

	// Route based on activity type
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
		// Unknown activity type, ignore
		return nil
	}
}

// Activity Handlers

func (s *Service) handleFollow(ctx context.Context, activity map[string]interface{}, remoteActor *domain.APRemoteActor) error {
	object, ok := activity["object"].(string)
	if !ok {
		return fmt.Errorf("invalid object in follow activity")
	}

	// Extract local username from object URI
	localUsername, err := s.extractUsernameFromURI(object)
	if err != nil {
		return fmt.Errorf("failed to extract username: %w", err)
	}

	// Get local user
	localUser, err := s.userRepo.GetByUsername(ctx, localUsername)
	if err != nil || localUser == nil {
		return fmt.Errorf("local user not found")
	}

	// Create follower relationship
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

	// Send Accept activity if auto-accept is enabled
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
		// Handle unfollow
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
		// Handle unlike
		activityID, ok := object["id"].(string)
		if !ok {
			return fmt.Errorf("missing id in undo like")
		}
		return s.repo.DeleteVideoReaction(ctx, activityID)

	case domain.ActivityTypeAnnounce:
		// Handle un-share
		activityID, ok := object["id"].(string)
		if !ok {
			return fmt.Errorf("missing id in undo announce")
		}
		return s.repo.DeleteVideoShare(ctx, activityID)

	default:
		// Unknown undo type, ignore
		return nil
	}
}

func (s *Service) handleAccept(ctx context.Context, activity map[string]interface{}, remoteActor *domain.APRemoteActor) error {
	// Handle accept of our follow request
	object, ok := activity["object"].(map[string]interface{})
	if !ok {
		return nil
	}

	objectType, ok := object["type"].(string)
	if !ok || objectType != domain.ActivityTypeFollow {
		return nil
	}

	// Update follower state
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
	// Handle rejection of our follow request
	object, ok := activity["object"].(map[string]interface{})
	if !ok {
		return nil
	}

	objectType, ok := object["type"].(string)
	if !ok || objectType != domain.ActivityTypeFollow {
		return nil
	}

	// Update or delete follower state
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

	// Extract video ID from object URI
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

	// Extract video ID from object URI
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
	// Handle creation of remote objects (comments, etc.)
	// For now, we'll just store the activity
	activityJSON, err := json.Marshal(activity)
	if err != nil {
		return fmt.Errorf("failed to marshal activity: %w", err)
	}

	// Validate activity has an ID
	if _, ok := activity["id"].(string); !ok {
		return fmt.Errorf("missing activity id")
	}

	apActivity := &domain.APActivity{
		ActorID:      remoteActor.ActorURI,
		Type:         domain.ActivityTypeCreate,
		Published:    time.Now(),
		ActivityJSON: activityJSON,
		Local:        false,
	}

	// Extract object info if possible
	if obj, ok := activity["object"].(map[string]interface{}); ok {
		if objID, ok := obj["id"].(string); ok {
			apActivity.ObjectID = &objID
		}
		if objType, ok := obj["type"].(string); ok {
			apActivity.ObjectType = &objType
		}
	}

	return s.repo.StoreActivity(ctx, apActivity)
}

func (s *Service) handleUpdate(ctx context.Context, activity map[string]interface{}, remoteActor *domain.APRemoteActor) error {
	// Handle updates to remote objects
	return s.handleCreate(ctx, activity, remoteActor) // Similar to create for now
}

func (s *Service) handleDelete(ctx context.Context, activity map[string]interface{}, remoteActor *domain.APRemoteActor) error {
	// Handle deletion of remote objects
	object, ok := activity["object"].(string)
	if !ok {
		return fmt.Errorf("invalid object in delete activity")
	}

	// Store the delete activity
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

// Activity Delivery

// DeliverActivity delivers an activity to a remote inbox
func (s *Service) DeliverActivity(ctx context.Context, actorID, inboxURL string, activity interface{}) error {
	// Get actor keys
	_, privateKey, err := s.repo.GetActorKeys(ctx, actorID)
	if err != nil {
		return fmt.Errorf("failed to get actor keys: %w", err)
	}

	// Serialize activity
	activityJSON, err := json.Marshal(activity)
	if err != nil {
		return fmt.Errorf("failed to marshal activity: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", inboxURL, strings.NewReader(string(activityJSON)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/activity+json")
	req.Header.Set("User-Agent", "Athena/1.0")

	// Get local actor to build key ID
	user, err := s.userRepo.GetByID(ctx, actorID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	keyID := s.buildActorID(user.Username) + "#main-key"

	// Sign request
	if err := activitypub.SignRequest(req, privateKey, keyID); err != nil {
		return fmt.Errorf("failed to sign request: %w", err)
	}

	// Send request
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to deliver activity: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delivery failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Helper Methods

func (s *Service) buildActorID(username string) string {
	return fmt.Sprintf("%s/users/%s", s.cfg.PublicBaseURL, username)
}

func (s *Service) extractUsernameFromURI(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("failed to parse URI: %w", err)
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "users" {
		return parts[1], nil
	}

	return "", fmt.Errorf("invalid actor URI format")
}

func (s *Service) extractVideoIDFromURI(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("failed to parse URI: %w", err)
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "videos" {
		return parts[1], nil
	}

	return "", fmt.Errorf("invalid video URI format")
}

func (s *Service) getOrCreateActorKeys(ctx context.Context, actorID string) (publicKey, privateKey string, err error) {
	// Try to get existing keys
	publicKey, privateKey, err = s.repo.GetActorKeys(ctx, actorID)
	if err == nil {
		return publicKey, privateKey, nil
	}

	// Generate new key pair
	publicKey, privateKey, err = activitypub.GenerateKeyPair()
	if err != nil {
		return "", "", fmt.Errorf("failed to generate key pair: %w", err)
	}

	// Store keys
	if err := s.repo.StoreActorKeys(ctx, actorID, publicKey, privateKey); err != nil {
		return "", "", fmt.Errorf("failed to store keys: %w", err)
	}

	return publicKey, privateKey, nil
}

// GetOutbox retrieves the outbox for an actor
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

	// Build ordered items
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

// GetFollowers retrieves the followers collection for an actor
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

	actorID := s.buildActorID(username)
	followersID := actorID + "/followers"

	// Build ordered items (just URIs)
	items := make([]interface{}, len(followers))
	for i, follower := range followers {
		items[i] = follower.FollowerID
	}

	collectionPage := &domain.OrderedCollectionPage{
		Context:      domain.ActivityStreamsContext,
		Type:         domain.ObjectTypeOrderedCollectionPage,
		ID:           fmt.Sprintf("%s?page=%d", followersID, page),
		TotalItems:   total,
		PartOf:       followersID,
		OrderedItems: items,
	}

	if page > 0 {
		collectionPage.Prev = fmt.Sprintf("%s?page=%d", followersID, page-1)
	}

	if offset+limit < total {
		collectionPage.Next = fmt.Sprintf("%s?page=%d", followersID, page+1)
	}

	return collectionPage, nil
}

// GetFollowing retrieves the following collection for an actor
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

	actorID := s.buildActorID(username)
	followingID := actorID + "/following"

	// Build ordered items (just URIs)
	items := make([]interface{}, len(following))
	for i, follow := range following {
		items[i] = follow.ActorID
	}

	collectionPage := &domain.OrderedCollectionPage{
		Context:      domain.ActivityStreamsContext,
		Type:         domain.ObjectTypeOrderedCollectionPage,
		ID:           fmt.Sprintf("%s?page=%d", followingID, page),
		TotalItems:   total,
		PartOf:       followingID,
		OrderedItems: items,
	}

	if page > 0 {
		collectionPage.Prev = fmt.Sprintf("%s?page=%d", followingID, page-1)
	}

	if offset+limit < total {
		collectionPage.Next = fmt.Sprintf("%s?page=%d", followingID, page+1)
	}

	return collectionPage, nil
}
