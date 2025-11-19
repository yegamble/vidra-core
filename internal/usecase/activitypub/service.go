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

	"github.com/google/uuid"

	"athena/internal/activitypub"
	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/port"
	"athena/internal/security"
)

const (
	// ActivityPubPublic is the special public audience in ActivityPub
	ActivityPubPublic = "https://www.w3.org/ns/activitystreams#Public"
)

// Service handles ActivityPub federation logic
type Service struct {
	repo         port.ActivityPubRepository
	userRepo     port.UserRepository
	videoRepo    port.VideoRepository
	commentRepo  port.CommentRepository
	cfg          *config.Config
	httpClient   *http.Client
	sigVerifier  *activitypub.HTTPSignatureVerifier
	urlValidator *security.URLValidator
}

// NewService creates a new ActivityPub service
func NewService(
	repo port.ActivityPubRepository,
	userRepo port.UserRepository,
	videoRepo port.VideoRepository,
	commentRepo port.CommentRepository,
	cfg *config.Config,
) *Service {
	return &Service{
		repo:         repo,
		userRepo:     userRepo,
		videoRepo:    videoRepo,
		commentRepo:  commentRepo,
		cfg:          cfg,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		sigVerifier:  activitypub.NewHTTPSignatureVerifier(),
		urlValidator: security.NewURLValidator(),
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

	// SSRF Protection: Validate URL before fetching
	if err := s.urlValidator.ValidateURL(actorURI); err != nil {
		return nil, fmt.Errorf("invalid or unsafe actor URI: %w", err)
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
	defer func() { _ = resp.Body.Close() }()

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
	// Handle creation of remote objects (videos, comments, etc.)
	activityJSON, err := json.Marshal(activity)
	if err != nil {
		return fmt.Errorf("failed to marshal activity: %w", err)
	}

	// Validate activity has an ID
	activityID, ok := activity["id"].(string)
	if !ok {
		return fmt.Errorf("missing activity id")
	}

	// Extract object
	obj, ok := activity["object"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("missing or invalid object in Create activity")
	}

	// Get object type
	objType, ok := obj["type"].(string)
	if !ok {
		return fmt.Errorf("missing object type")
	}

	objID, _ := obj["id"].(string)

	// Create activity record
	apActivity := &domain.APActivity{
		ActorID:      remoteActor.ActorURI,
		Type:         domain.ActivityTypeCreate,
		Published:    time.Now(),
		ActivityJSON: activityJSON,
		Local:        false,
		ObjectID:     &objID,
		ObjectType:   &objType,
	}

	// Store the activity
	if err := s.repo.StoreActivity(ctx, apActivity); err != nil {
		return fmt.Errorf("failed to store activity: %w", err)
	}

	// Handle specific object types
	switch objType {
	case "Video":
		return s.ingestRemoteVideo(ctx, obj, remoteActor, activityID)
	case "Note":
		// Handle remote comments/notes (already implemented elsewhere)
		return nil
	default:
		// Unknown object type - activity is stored but not processed
		return nil
	}
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
	// Set Host header explicitly for HTTP signature verification
	req.Header.Set("Host", req.URL.Host)

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
	defer func() { _ = resp.Body.Close() }()

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

// buildFollowCollectionPage is a helper to build followers/following collection pages
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

	// Build ordered items (just URIs)
	items := make([]interface{}, len(followers))
	for i, follower := range followers {
		items[i] = follower.FollowerID
	}

	return s.buildFollowCollectionPage(username, "followers", page, limit, total, items), nil
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

	// Build ordered items (just URIs)
	items := make([]interface{}, len(following))
	for i, follow := range following {
		items[i] = follow.ActorID
	}

	return s.buildFollowCollectionPage(username, "following", page, limit, total, items), nil
}

// Comment Publishing

// BuildNoteObject converts a domain.Comment to an ActivityPub NoteObject
func (s *Service) BuildNoteObject(ctx context.Context, comment *domain.Comment) (*domain.NoteObject, error) {
	if comment == nil {
		return nil, fmt.Errorf("comment is nil")
	}

	// Get the comment author
	user, err := s.userRepo.GetByID(ctx, comment.UserID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get comment author: %w", err)
	}

	// Get the video the comment is on
	video, err := s.videoRepo.GetByID(ctx, comment.VideoID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get video: %w", err)
	}

	// Build the note ID
	noteID := fmt.Sprintf("%s/comments/%s", s.cfg.PublicBaseURL, comment.ID.String())

	// Build the attributedTo (comment author)
	attributedTo := s.buildActorID(user.Username)

	// Build inReplyTo - if this is a nested comment, point to parent comment; otherwise point to video
	var inReplyTo string
	if comment.ParentID != nil {
		inReplyTo = fmt.Sprintf("%s/comments/%s", s.cfg.PublicBaseURL, comment.ParentID.String())
	} else {
		inReplyTo = fmt.Sprintf("%s/videos/%s", s.cfg.PublicBaseURL, comment.VideoID.String())
	}

	// Build the note object
	note := &domain.NoteObject{
		Type:         domain.ObjectTypeNote,
		ID:           noteID,
		Content:      comment.Body,
		Published:    &comment.CreatedAt,
		AttributedTo: attributedTo,
		InReplyTo:    inReplyTo,
	}

	// Set updated time if edited
	if comment.EditedAt != nil {
		note.Updated = comment.EditedAt
	}

	// Set audience based on video privacy
	switch video.Privacy {
	case domain.PrivacyPublic:
		note.To = []string{"https://www.w3.org/ns/activitystreams#Public"}
	case domain.PrivacyUnlisted:
		note.Cc = []string{"https://www.w3.org/ns/activitystreams#Public"}
	}

	// Add video owner to Cc if they're not the comment author
	if video.UserID != user.ID {
		videoOwner, err := s.userRepo.GetByID(ctx, video.UserID)
		if err == nil && videoOwner != nil {
			videoOwnerURI := s.buildActorID(videoOwner.Username)
			note.Cc = append(note.Cc, videoOwnerURI)
		}
	}

	return note, nil
}

// CreateCommentActivity wraps a NoteObject in a Create activity
func (s *Service) CreateCommentActivity(ctx context.Context, comment *domain.Comment) (*domain.Activity, error) {
	// Build the note object
	note, err := s.BuildNoteObject(ctx, comment)
	if err != nil {
		return nil, fmt.Errorf("failed to build note object: %w", err)
	}

	// Get the comment author
	user, err := s.userRepo.GetByID(ctx, comment.UserID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get comment author: %w", err)
	}

	// Build the Create activity
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

// PublishComment publishes a comment to ActivityPub followers
func (s *Service) PublishComment(ctx context.Context, commentID string) error {
	// For now, return an error indicating this needs a comment repository
	// The tests will need to be updated to provide a way to fetch comments
	if s.commentRepo == nil {
		return fmt.Errorf("comment repository not configured")
	}

	// Parse comment ID
	commentUUID, err := uuid.Parse(commentID)
	if err != nil {
		return fmt.Errorf("invalid comment ID: %w", err)
	}

	// Get the comment
	comment, err := s.commentRepo.GetByID(ctx, commentUUID)
	if err != nil {
		return fmt.Errorf("failed to get comment: %w", err)
	}

	// Don't publish deleted comments
	if comment.Status == domain.CommentStatusDeleted {
		return fmt.Errorf("cannot publish deleted comment")
	}

	// Create the activity
	activity, err := s.CreateCommentActivity(ctx, comment)
	if err != nil {
		return fmt.Errorf("failed to create comment activity: %w", err)
	}

	// Convert to APActivity for storage
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

	// Store the activity
	if err := s.repo.StoreActivity(ctx, apActivity); err != nil {
		return fmt.Errorf("failed to store activity: %w", err)
	}

	// Get the video to find its owner
	video, err := s.videoRepo.GetByID(ctx, comment.VideoID.String())
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}

	// Get video owner's followers for delivery
	followers, _, err := s.repo.GetFollowers(ctx, video.UserID, "accepted", 100, 0)
	if err != nil {
		return fmt.Errorf("failed to get followers: %w", err)
	}

	// Enqueue delivery to each follower
	for _, follower := range followers {
		remoteActor, err := s.repo.GetRemoteActor(ctx, follower.FollowerID)
		if err != nil {
			continue
		}

		delivery := &domain.APDeliveryQueue{
			ActivityID:  apActivity.ID,
			InboxURL:    remoteActor.InboxURL,
			ActorID:     comment.UserID.String(),
			Attempts:    0,
			MaxAttempts: 3,
			NextAttempt: time.Now(),
			Status:      "pending",
		}

		if err := s.repo.EnqueueDelivery(ctx, delivery); err != nil {
			// Log but don't fail on delivery queue errors
			continue
		}
	}

	return nil
}

// UpdateComment publishes an Update activity for an edited comment
func (s *Service) UpdateComment(ctx context.Context, commentID string) error {
	if s.commentRepo == nil {
		return fmt.Errorf("comment repository not configured")
	}

	// Parse comment ID
	commentUUID, err := uuid.Parse(commentID)
	if err != nil {
		return fmt.Errorf("invalid comment ID: %w", err)
	}

	// Get the comment
	comment, err := s.commentRepo.GetByID(ctx, commentUUID)
	if err != nil {
		return fmt.Errorf("failed to get comment: %w", err)
	}

	// Build the note object
	note, err := s.BuildNoteObject(ctx, comment)
	if err != nil {
		return fmt.Errorf("failed to build note object: %w", err)
	}

	// Get the comment author
	user, err := s.userRepo.GetByID(ctx, comment.UserID.String())
	if err != nil {
		return fmt.Errorf("failed to get comment author: %w", err)
	}

	// Build the Update activity
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

	// Marshal to JSON for storage
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

	// Store the activity
	if err := s.repo.StoreActivity(ctx, apActivity); err != nil {
		return fmt.Errorf("failed to store activity: %w", err)
	}

	// Get the video to find its owner
	video, err := s.videoRepo.GetByID(ctx, comment.VideoID.String())
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}

	// Get video owner's followers for delivery
	followers, _, err := s.repo.GetFollowers(ctx, video.UserID, "accepted", 100, 0)
	if err != nil {
		return fmt.Errorf("failed to get followers: %w", err)
	}

	// Enqueue delivery to each follower
	for _, follower := range followers {
		remoteActor, err := s.repo.GetRemoteActor(ctx, follower.FollowerID)
		if err != nil {
			continue
		}

		delivery := &domain.APDeliveryQueue{
			ActivityID:  apActivity.ID,
			InboxURL:    remoteActor.InboxURL,
			ActorID:     comment.UserID.String(),
			Attempts:    0,
			MaxAttempts: 3,
			NextAttempt: time.Now(),
			Status:      "pending",
		}

		if err := s.repo.EnqueueDelivery(ctx, delivery); err != nil {
			continue
		}
	}

	return nil
}

// DeleteComment publishes a Delete activity for a deleted comment
func (s *Service) DeleteComment(ctx context.Context, commentID string) error {
	if s.commentRepo == nil {
		return fmt.Errorf("comment repository not configured")
	}

	// Parse comment ID
	commentUUID, err := uuid.Parse(commentID)
	if err != nil {
		return fmt.Errorf("invalid comment ID: %w", err)
	}

	// Get the comment
	comment, err := s.commentRepo.GetByID(ctx, commentUUID)
	if err != nil {
		return fmt.Errorf("failed to get comment: %w", err)
	}

	// Get the comment author
	user, err := s.userRepo.GetByID(ctx, comment.UserID.String())
	if err != nil {
		return fmt.Errorf("failed to get comment author: %w", err)
	}

	// Get the video
	video, err := s.videoRepo.GetByID(ctx, comment.VideoID.String())
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}

	// Build the Delete activity
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

	// Marshal to JSON for storage
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

	// Store the activity
	if err := s.repo.StoreActivity(ctx, apActivity); err != nil {
		return fmt.Errorf("failed to store activity: %w", err)
	}

	// Get video owner's followers for delivery
	followers, _, err := s.repo.GetFollowers(ctx, video.UserID, "accepted", 100, 0)
	if err != nil {
		return fmt.Errorf("failed to get followers: %w", err)
	}

	// Enqueue delivery to each follower
	for _, follower := range followers {
		remoteActor, err := s.repo.GetRemoteActor(ctx, follower.FollowerID)
		if err != nil {
			continue
		}

		delivery := &domain.APDeliveryQueue{
			ActivityID:  apActivity.ID,
			InboxURL:    remoteActor.InboxURL,
			ActorID:     comment.UserID.String(),
			Attempts:    0,
			MaxAttempts: 3,
			NextAttempt: time.Now(),
			Status:      "pending",
		}

		if err := s.repo.EnqueueDelivery(ctx, delivery); err != nil {
			continue
		}
	}

	return nil
}

// Video Publishing

// BuildVideoObject converts a domain.Video to an ActivityPub VideoObject
func (s *Service) BuildVideoObject(ctx context.Context, video *domain.Video) (*domain.VideoObject, error) {
	if video == nil {
		return nil, fmt.Errorf("video is nil")
	}

	// Get video owner
	owner, err := s.userRepo.GetByID(ctx, video.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get video owner: %w", err)
	}

	// Build video URL
	videoID := fmt.Sprintf("%s/videos/%s", s.cfg.PublicBaseURL, video.ID)
	actorID := s.buildActorID(owner.Username)

	// Build VideoObject
	videoObj := &domain.VideoObject{
		Context:      []interface{}{domain.ActivityStreamsContext, domain.PeerTubeContext},
		Type:         domain.ObjectTypeVideo,
		ID:           videoID,
		Name:         video.Title,
		UUID:         video.ID,
		Published:    &video.UploadDate,
		Updated:      &video.UpdatedAt,
		AttributedTo: []string{actorID},
		State:        1, // PeerTube state: 1 = published
	}

	// Description and metadata
	if video.Description != "" {
		videoObj.Content = video.Description
		videoObj.Summary = video.Description
	}

	// Duration in ISO 8601 format (PT1H2M3S)
	if video.Duration > 0 {
		hours := video.Duration / 3600
		minutes := (video.Duration % 3600) / 60
		seconds := video.Duration % 60
		if hours > 0 {
			videoObj.Duration = fmt.Sprintf("PT%dH%dM%dS", hours, minutes, seconds)
		} else if minutes > 0 {
			videoObj.Duration = fmt.Sprintf("PT%dM%dS", minutes, seconds)
		} else {
			videoObj.Duration = fmt.Sprintf("PT%dS", seconds)
		}
	}

	// Privacy settings
	videoObj.CommentsEnabled = true
	videoObj.DownloadEnabled = true
	videoObj.Sensitive = video.Privacy == domain.PrivacyPrivate
	videoObj.WaitTranscoding = video.Status == domain.StatusProcessing

	// View count (if available)
	videoObj.Views = int(video.Views)

	// Tags - convert to hashtags for ActivityPub
	if len(video.Tags) > 0 {
		videoObj.Tag = make([]domain.APTag, len(video.Tags))
		for i, tag := range video.Tags {
			videoObj.Tag[i] = domain.APTag{
				Type: "Hashtag",
				Name: "#" + tag,
			}
		}
	}

	// Category (PeerTube compatibility)
	if video.Category != nil {
		videoObj.Category = &domain.APCategory{
			Identifier: video.Category.ID.String(),
			Name:       video.Category.Name,
		}
	}

	// Language (PeerTube compatibility)
	if video.Language != "" {
		videoObj.Language = &domain.APLanguage{
			Identifier: video.Language,
		}
	}

	// Build URLs for video files
	if len(video.OutputPaths) > 0 {
		// MP4 video file
		mp4URL := domain.APUrl{
			Type:      "Link",
			MediaType: "video/mp4",
			Href:      fmt.Sprintf("%s/videos/%s/stream", s.cfg.PublicBaseURL, video.ID),
			Height:    video.Metadata.Height,
			Width:     video.Metadata.Width,
		}
		videoObj.URL = append(videoObj.URL, mp4URL)

		// HLS master playlist
		hlsURL := domain.APUrl{
			Type:      "Link",
			MediaType: "application/x-mpegURL",
			Href:      fmt.Sprintf("%s/videos/%s/master.m3u8", s.cfg.PublicBaseURL, video.ID),
		}
		videoObj.URL = append(videoObj.URL, hlsURL)

		// Add individual quality variants from OutputPaths
		for quality, path := range video.OutputPaths {
			variantURL := domain.APUrl{
				Type:      "Link",
				MediaType: "application/x-mpegURL",
				Href:      fmt.Sprintf("%s%s", s.cfg.PublicBaseURL, path),
			}
			// Parse resolution from quality string (e.g., "1080p" -> 1080)
			var height int
			if _, err := fmt.Sscanf(quality, "%dp", &height); err == nil {
				variantURL.Height = height
				// Assume 16:9 aspect ratio for width if not available
				if video.Metadata.Width > 0 && video.Metadata.Height > 0 {
					variantURL.Width = (height * video.Metadata.Width) / video.Metadata.Height
				} else {
					variantURL.Width = (height * 16) / 9
				}
			}
			videoObj.URL = append(videoObj.URL, variantURL)
		}
	}

	// Thumbnail
	if video.ThumbnailPath != "" {
		// Handle both absolute paths and relative paths
		thumbnailURL := video.ThumbnailPath
		if !strings.HasPrefix(thumbnailURL, "http") {
			// Remove leading slash if present to avoid double slashes
			thumbnailURL = strings.TrimPrefix(thumbnailURL, "/")
			thumbnailURL = fmt.Sprintf("%s/%s", s.cfg.PublicBaseURL, thumbnailURL)
		}
		icon := domain.Image{
			Type:      "Image",
			URL:       thumbnailURL,
			MediaType: "image/jpeg",
			// Width and Height are optional for thumbnails, omit if not available
		}
		videoObj.Icon = []domain.Image{icon}
	}

	// Federation audience (To/Cc)
	switch video.Privacy {
	case domain.PrivacyPublic:
		videoObj.To = []string{ActivityPubPublic}
		videoObj.Cc = []string{actorID + "/followers"}
	case domain.PrivacyUnlisted:
		videoObj.To = []string{actorID + "/followers"}
		videoObj.Cc = []string{ActivityPubPublic}
	case domain.PrivacyPrivate:
		// Private videos only go to followers
		videoObj.To = []string{actorID + "/followers"}
	}

	// Collection endpoints
	videoObj.Likes = videoID + "/likes"
	videoObj.Dislikes = videoID + "/dislikes"
	videoObj.Shares = videoID + "/shares"
	videoObj.Comments = videoID + "/comments"

	return videoObj, nil
}

// CreateVideoActivity wraps a VideoObject in a Create activity
func (s *Service) CreateVideoActivity(ctx context.Context, video *domain.Video) (*domain.Activity, error) {
	// Build the video object
	videoObj, err := s.BuildVideoObject(ctx, video)
	if err != nil {
		return nil, fmt.Errorf("failed to build video object: %w", err)
	}

	// Get video owner
	owner, err := s.userRepo.GetByID(ctx, video.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get video owner: %w", err)
	}

	actorID := s.buildActorID(owner.Username)
	activityID := fmt.Sprintf("%s/activities/create-%s", s.cfg.PublicBaseURL, video.ID)

	// Activity is published at the current time
	now := time.Now()

	// Create the activity
	activity := &domain.Activity{
		Context:   []interface{}{domain.ActivityStreamsContext},
		Type:      domain.ActivityTypeCreate,
		ID:        activityID,
		Actor:     actorID,
		Object:    videoObj,
		Published: &now,
		To:        videoObj.To,
		Cc:        videoObj.Cc,
	}

	return activity, nil
}

// PublishVideo publishes a video to ActivityPub followers
func (s *Service) PublishVideo(ctx context.Context, videoID string) error {
	// Get the video
	video, err := s.videoRepo.GetByID(ctx, videoID)
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}
	if video == nil {
		return fmt.Errorf("video not found")
	}

	// Only publish completed videos
	if video.Status != domain.StatusCompleted {
		return fmt.Errorf("video not completed")
	}

	// Create the activity
	activity, err := s.CreateVideoActivity(ctx, video)
	if err != nil {
		return fmt.Errorf("failed to create activity: %w", err)
	}

	// Get followers to deliver to (accepted followers only)
	followers, _, err := s.repo.GetFollowers(ctx, video.UserID, "accepted", 1000, 0)
	if err != nil {
		return fmt.Errorf("failed to get followers: %w", err)
	}

	// Queue delivery to each follower's inbox
	for _, follower := range followers {
		// Get remote actor details
		remoteActor, err := s.repo.GetRemoteActor(ctx, follower.FollowerID)
		if err != nil {
			continue // Skip if we can't get the remote actor
		}

		// Determine inbox URL (prefer shared inbox)
		inboxURL := remoteActor.InboxURL
		if remoteActor.SharedInbox != nil && *remoteActor.SharedInbox != "" {
			inboxURL = *remoteActor.SharedInbox
		}

		// Queue the delivery for async processing
		delivery := &domain.APDeliveryQueue{
			ActivityID: activity.ID,
			ActorID:    activity.Actor,
			InboxURL:   inboxURL,
		}

		if err := s.repo.EnqueueDelivery(ctx, delivery); err != nil {
			// Log error but continue with other followers
			continue
		}
	}

	// Store the activity in the outbox
	apActivity := &domain.APActivity{
		ID:           activity.ID,
		ActorID:      activity.Actor,
		Type:         activity.Type,
		Published:    *activity.Published,
		ActivityJSON: nil, // Will be marshaled by repository
		Local:        true,
	}

	if err := s.repo.StoreActivity(ctx, apActivity); err != nil {
		// Activity is already queued, so just log this error
		return fmt.Errorf("failed to store activity: %w", err)
	}

	return nil
}

// UpdateVideo publishes an Update activity for an edited video
func (s *Service) UpdateVideo(ctx context.Context, videoID string) error {
	// Get the video
	video, err := s.videoRepo.GetByID(ctx, videoID)
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}
	if video == nil {
		return fmt.Errorf("video not found")
	}

	// Only update public and unlisted videos
	if video.Privacy == "private" {
		return nil
	}

	// Build the updated video object
	videoObj, err := s.BuildVideoObject(ctx, video)
	if err != nil {
		return fmt.Errorf("failed to build video object: %w", err)
	}

	// Get video owner
	owner, err := s.userRepo.GetByID(ctx, video.UserID)
	if err != nil {
		return fmt.Errorf("failed to get video owner: %w", err)
	}

	actorID := s.buildActorID(owner.Username)
	activityID := fmt.Sprintf("%s/videos/%s/activity/update-%d", s.cfg.PublicBaseURL, video.ID, time.Now().Unix())

	// Create Update activity
	now := time.Now()
	activity := &domain.Activity{
		Context:   []interface{}{domain.ActivityStreamsContext},
		Type:      domain.ActivityTypeUpdate,
		ID:        activityID,
		Actor:     actorID,
		Object:    videoObj,
		Published: &now,
		To:        videoObj.To,
		Cc:        videoObj.Cc,
	}

	// Get followers and deliver update activity
	followers, _, err := s.repo.GetFollowers(ctx, video.UserID, "accepted", 1000, 0)
	if err != nil {
		return fmt.Errorf("failed to get followers: %w", err)
	}

	for _, follower := range followers {
		remoteActor, err := s.repo.GetRemoteActor(ctx, follower.FollowerID)
		if err != nil {
			continue
		}

		inboxURL := remoteActor.InboxURL
		if remoteActor.SharedInbox != nil && *remoteActor.SharedInbox != "" {
			inboxURL = *remoteActor.SharedInbox
		}

		delivery := &domain.APDeliveryQueue{
			ActivityID: activity.ID,
			ActorID:    activity.Actor,
			InboxURL:   inboxURL,
		}

		if err := s.repo.EnqueueDelivery(ctx, delivery); err != nil {
			continue
		}
	}

	// Store the activity in the outbox
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

// DeleteVideo publishes a Delete activity for a deleted video
func (s *Service) DeleteVideo(ctx context.Context, videoID string) error {
	// Get the video (it should still exist briefly before actual deletion)
	video, err := s.videoRepo.GetByID(ctx, videoID)
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}
	if video == nil {
		return fmt.Errorf("video not found")
	}

	// Get video owner
	owner, err := s.userRepo.GetByID(ctx, video.UserID)
	if err != nil {
		return fmt.Errorf("failed to get video owner: %w", err)
	}

	actorID := s.buildActorID(owner.Username)
	videoObjectID := fmt.Sprintf("%s/videos/%s", s.cfg.PublicBaseURL, video.ID)
	activityID := fmt.Sprintf("%s/videos/%s/activity/delete-%d", s.cfg.PublicBaseURL, video.ID, time.Now().Unix())

	// Create Delete activity
	now := time.Now()
	activity := &domain.Activity{
		Context:   []interface{}{domain.ActivityStreamsContext},
		Type:      domain.ActivityTypeDelete,
		ID:        activityID,
		Actor:     actorID,
		Object:    videoObjectID, // Just the ID, not the full object
		Published: &now,
		To:        []string{ActivityPubPublic},
		Cc:        []string{actorID + "/followers"},
	}

	// Get followers and deliver delete activity
	followers, _, err := s.repo.GetFollowers(ctx, video.UserID, "accepted", 1000, 0)
	if err != nil {
		return fmt.Errorf("failed to get followers: %w", err)
	}

	for _, follower := range followers {
		remoteActor, err := s.repo.GetRemoteActor(ctx, follower.FollowerID)
		if err != nil {
			continue
		}

		inboxURL := remoteActor.InboxURL
		if remoteActor.SharedInbox != nil && *remoteActor.SharedInbox != "" {
			inboxURL = *remoteActor.SharedInbox
		}

		delivery := &domain.APDeliveryQueue{
			ActivityID: activity.ID,
			ActorID:    activity.Actor,
			InboxURL:   inboxURL,
		}

		if err := s.repo.EnqueueDelivery(ctx, delivery); err != nil {
			continue
		}
	}

	// Store the activity in the outbox
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

// ingestRemoteVideo processes a remote Video object from a Create activity
func (s *Service) ingestRemoteVideo(ctx context.Context, videoObj map[string]interface{}, remoteActor *domain.APRemoteActor, activityID string) error {
	// Extract video URI
	videoURI, ok := videoObj["id"].(string)
	if !ok {
		return fmt.Errorf("missing video id")
	}

	// Check if we already have this video
	existingVideo, err := s.videoRepo.GetByRemoteURI(ctx, videoURI)
	if err == nil && existingVideo != nil {
		// Video already exists, update it instead
		return s.updateRemoteVideo(ctx, videoObj, existingVideo, remoteActor)
	}

	// Extract video metadata
	title, _ := videoObj["name"].(string)
	if title == "" {
		title = "Untitled Remote Video"
	}

	description, _ := videoObj["content"].(string)
	if description == "" {
		description, _ = videoObj["summary"].(string)
	}

	// Parse duration (ISO 8601 format like "PT5M30S")
	duration := 0
	if durationStr, ok := videoObj["duration"].(string); ok {
		duration = parseDuration(durationStr)
	}

	// Extract video URL (prefer MP4, then any video URL)
	videoURL := extractVideoURL(videoObj)
	if videoURL == "" {
		return fmt.Errorf("no video URL found in remote video object")
	}

	// Extract thumbnail URL
	thumbnailURL := extractThumbnailURL(videoObj)

	// Extract instance domain from video URI
	instanceDomain := extractDomain(videoURI)

	// Determine privacy level
	privacy := determinePrivacy(videoObj)

	// Extract publish date
	uploadDate := time.Now()
	if published, ok := videoObj["published"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339, published); err == nil {
			uploadDate = parsed
		}
	}

	// Extract language
	language := "en" // default
	if lang, ok := videoObj["language"].(map[string]interface{}); ok {
		if identifier, ok := lang["identifier"].(string); ok {
			language = identifier
		}
	} else if langStr, ok := videoObj["language"].(string); ok {
		language = langStr
	}

	// Extract tags
	tags := extractTags(videoObj)

	// Create remote video record
	now := time.Now()
	video := &domain.Video{
		ID:                   uuid.New().String(),
		Title:                title,
		Description:          description,
		Duration:             duration,
		Privacy:              privacy,
		Status:               domain.StatusCompleted,
		UploadDate:           uploadDate,
		Tags:                 tags,
		Language:             language,
		IsRemote:             true,
		RemoteURI:            &videoURI,
		RemoteActorURI:       &remoteActor.ActorURI,
		RemoteVideoURL:       &videoURL,
		RemoteInstanceDomain: &instanceDomain,
		RemoteThumbnailURL:   &thumbnailURL,
		RemoteLastSyncedAt:   &now,
		CreatedAt:            uploadDate,
		UpdatedAt:            now,
	}

	// Store the remote video
	if err := s.videoRepo.CreateRemoteVideo(ctx, video); err != nil {
		return fmt.Errorf("failed to create remote video: %w", err)
	}

	return nil
}

// updateRemoteVideo updates an existing remote video with new metadata
func (s *Service) updateRemoteVideo(ctx context.Context, videoObj map[string]interface{}, existingVideo *domain.Video, remoteActor *domain.APRemoteActor) error {
	// Extract updated metadata
	if title, ok := videoObj["name"].(string); ok && title != "" {
		existingVideo.Title = title
	}

	if description, ok := videoObj["content"].(string); ok {
		existingVideo.Description = description
	} else if summary, ok := videoObj["summary"].(string); ok {
		existingVideo.Description = summary
	}

	// Update duration if changed
	if durationStr, ok := videoObj["duration"].(string); ok {
		existingVideo.Duration = parseDuration(durationStr)
	}

	// Update video URL
	if videoURL := extractVideoURL(videoObj); videoURL != "" {
		existingVideo.RemoteVideoURL = &videoURL
	}

	// Update thumbnail
	if thumbnailURL := extractThumbnailURL(videoObj); thumbnailURL != "" {
		existingVideo.RemoteThumbnailURL = &thumbnailURL
	}

	// Update privacy
	existingVideo.Privacy = determinePrivacy(videoObj)

	// Update tags
	existingVideo.Tags = extractTags(videoObj)

	// Update last synced timestamp
	now := time.Now()
	existingVideo.RemoteLastSyncedAt = &now
	existingVideo.UpdatedAt = now

	// Save updates
	if err := s.videoRepo.Update(ctx, existingVideo); err != nil {
		return fmt.Errorf("failed to update remote video: %w", err)
	}

	return nil
}

// Helper functions for extracting video metadata

// parseDuration parses ISO 8601 duration (e.g., "PT5M30S") to seconds
func parseDuration(durationStr string) int {
	duration := 0
	if len(durationStr) < 3 || !strings.HasPrefix(durationStr, "PT") {
		return 0
	}

	durationStr = durationStr[2:] // Remove "PT"

	// Parse hours
	if idx := strings.Index(durationStr, "H"); idx > 0 {
		hours := 0
		_, _ = fmt.Sscanf(durationStr[:idx], "%d", &hours)
		duration += hours * 3600
		durationStr = durationStr[idx+1:]
	}

	// Parse minutes
	if idx := strings.Index(durationStr, "M"); idx > 0 {
		minutes := 0
		_, _ = fmt.Sscanf(durationStr[:idx], "%d", &minutes)
		duration += minutes * 60
		durationStr = durationStr[idx+1:]
	}

	// Parse seconds
	if idx := strings.Index(durationStr, "S"); idx > 0 {
		seconds := 0
		_, _ = fmt.Sscanf(durationStr[:idx], "%d", &seconds)
		duration += seconds
	}

	return duration
}

// extractVideoURL extracts the best video URL from a VideoObject
func extractVideoURL(videoObj map[string]interface{}) string {
	// Try URL array first (PeerTube format)
	if urls, ok := videoObj["url"].([]interface{}); ok {
		// Prefer MP4
		for _, u := range urls {
			if urlObj, ok := u.(map[string]interface{}); ok {
				if mediaType, ok := urlObj["mediaType"].(string); ok && mediaType == "video/mp4" {
					if href, ok := urlObj["href"].(string); ok {
						return href
					}
				}
			}
		}
		// Fall back to first video URL
		for _, u := range urls {
			if urlObj, ok := u.(map[string]interface{}); ok {
				if mediaType, ok := urlObj["mediaType"].(string); ok && strings.HasPrefix(mediaType, "video/") {
					if href, ok := urlObj["href"].(string); ok {
						return href
					}
				}
			}
		}
	}

	// Try single URL string
	if url, ok := videoObj["url"].(string); ok {
		return url
	}

	return ""
}

// extractThumbnailURL extracts thumbnail URL from VideoObject
func extractThumbnailURL(videoObj map[string]interface{}) string {
	// Try icon first
	if icon, ok := videoObj["icon"].(map[string]interface{}); ok {
		if url, ok := icon["url"].(string); ok {
			return url
		}
	}

	// Try image
	if image, ok := videoObj["image"].(map[string]interface{}); ok {
		if url, ok := image["url"].(string); ok {
			return url
		}
	}

	// Try preview
	if preview, ok := videoObj["preview"].(map[string]interface{}); ok {
		if url, ok := preview["url"].(string); ok {
			return url
		}
	}

	return ""
}

// extractDomain extracts domain from a URL
func extractDomain(uri string) string {
	parsedURL, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	return parsedURL.Host
}

// determinePrivacy determines privacy level from ActivityPub audience fields
func determinePrivacy(videoObj map[string]interface{}) domain.Privacy {
	// Check "to" field
	to := []string{}
	if toField, ok := videoObj["to"].([]interface{}); ok {
		for _, t := range toField {
			if str, ok := t.(string); ok {
				to = append(to, str)
			}
		}
	}

	// Check "cc" field
	cc := []string{}
	if ccField, ok := videoObj["cc"].([]interface{}); ok {
		for _, c := range ccField {
			if str, ok := c.(string); ok {
				cc = append(cc, str)
			}
		}
	}

	// Public if sent to public collection
	for _, t := range to {
		if t == ActivityPubPublic || t == "Public" || t == "as:Public" {
			return domain.PrivacyPublic
		}
	}

	// Unlisted if cc'd to public
	for _, c := range cc {
		if c == ActivityPubPublic || c == "Public" || c == "as:Public" {
			return domain.PrivacyUnlisted
		}
	}

	// Default to private
	return domain.PrivacyPrivate
}

// extractTags extracts tags from VideoObject
func extractTags(videoObj map[string]interface{}) []string {
	tags := []string{}

	if tagField, ok := videoObj["tag"].([]interface{}); ok {
		for _, t := range tagField {
			if tagObj, ok := t.(map[string]interface{}); ok {
				if tagName, ok := tagObj["name"].(string); ok {
					tagName = strings.TrimPrefix(tagName, "#")
					tags = append(tags, tagName)
				}
			} else if tagStr, ok := t.(string); ok {
				tags = append(tags, strings.TrimPrefix(tagStr, "#"))
			}
		}
	}

	return tags
}
