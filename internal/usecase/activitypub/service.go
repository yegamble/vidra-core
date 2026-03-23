package activitypub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

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
