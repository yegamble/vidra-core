package social

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/security"
)

// SocialRepository defines the interface for social data persistence
type SocialRepository interface {
	UpsertActor(ctx context.Context, actor *domain.ATProtoActor) error
	GetActorByDID(ctx context.Context, did string) (*domain.ATProtoActor, error)
	GetActorByHandle(ctx context.Context, handle string) (*domain.ATProtoActor, error)
	CreateFollow(ctx context.Context, follow *domain.Follow) error
	RevokeFollow(ctx context.Context, uri string) error
	GetFollowers(ctx context.Context, did string, limit, offset int) ([]domain.Follow, error)
	GetFollowing(ctx context.Context, did string, limit, offset int) ([]domain.Follow, error)
	GetFollow(ctx context.Context, followerDID, followingDID string) (*domain.Follow, error)
	IsFollowing(ctx context.Context, followerDID, followingDID string) (bool, error)
	CreateLike(ctx context.Context, like *domain.Like) error
	DeleteLike(ctx context.Context, uri string) error
	GetLikes(ctx context.Context, subjectURI string, limit, offset int) ([]domain.Like, error)
	HasLiked(ctx context.Context, actorDID, subjectURI string) (bool, error)
	CreateComment(ctx context.Context, comment *domain.SocialComment) error
	DeleteComment(ctx context.Context, uri string) error
	GetComments(ctx context.Context, rootURI string, limit, offset int) ([]domain.SocialComment, error)
	GetCommentThread(ctx context.Context, parentURI string, limit, offset int) ([]domain.SocialComment, error)
	CreateModerationLabel(ctx context.Context, label *domain.ModerationLabel) error
	RemoveModerationLabel(ctx context.Context, id string) error
	GetModerationLabels(ctx context.Context, actorDID string) ([]domain.ModerationLabel, error)
	GetSocialStats(ctx context.Context, did string) (*domain.SocialStats, error)
	GetBlockedLabels(ctx context.Context) ([]string, error)
}

// AtprotoPublisher publishes activity to ATProto (optional).
// Keep this minimal to avoid import cycles.
type AtprotoPublisher interface {
	PublishVideo(ctx context.Context, v *domain.Video) error
	StartBackgroundRefresh(ctx context.Context, interval time.Duration)
}

// Service handles ATProto social interactions
type Service struct {
	cfg          *config.Config
	socialRepo   SocialRepository
	atproto      AtprotoPublisher
	client       *http.Client
	encKey       []byte
	urlValidator *security.URLValidator
}

// NewService creates a new social service instance
func NewService(
	cfg *config.Config,
	socialRepo SocialRepository,
	atproto AtprotoPublisher,
	encKey []byte,
) *Service {
	return &Service{
		cfg:          cfg,
		socialRepo:   socialRepo,
		atproto:      atproto,
		client:       &http.Client{Timeout: 10 * time.Second},
		encKey:       encKey,
		urlValidator: security.NewURLValidator(),
	}
}

// FollowRecord represents an ATProto follow record
type FollowRecord struct {
	Type      string    `json:"$type"`
	Subject   string    `json:"subject"`
	CreatedAt time.Time `json:"createdAt"`
}

// LikeRecord represents an ATProto like record
type LikeRecord struct {
	Type      string    `json:"$type"`
	Subject   Subject   `json:"subject"`
	CreatedAt time.Time `json:"createdAt"`
}

// Subject represents a subject reference in ATProto
type Subject struct {
	URI string `json:"uri"`
	CID string `json:"cid,omitempty"`
}

// CommentRecord represents an ATProto post record used as a reply
type CommentRecord struct {
	Type      string    `json:"$type"`
	Text      string    `json:"text"`
	Reply     *Reply    `json:"reply,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// Reply represents reply parent/root references
type Reply struct {
	Root   Subject `json:"root"`
	Parent Subject `json:"parent"`
}

// Follow creates a follow relationship via ATProto
func (s *Service) Follow(ctx context.Context, followerDID, targetHandle string) error {
	// Resolve target actor
	targetActor, err := s.resolveActor(ctx, targetHandle)
	if err != nil {
		return fmt.Errorf("resolve target actor: %w", err)
	}

	// Check if already following
	isFollowing, _ := s.socialRepo.IsFollowing(ctx, followerDID, targetActor.DID)
	if isFollowing {
		return fmt.Errorf("already following")
	}

	// Create follow record in ATProto
	record := FollowRecord{
		Type:      "app.bsky.graph.follow",
		Subject:   targetActor.DID,
		CreatedAt: time.Now().UTC(),
	}

	uri, cid, err := s.createRecord(ctx, followerDID, "app.bsky.graph.follow", record)
	if err != nil {
		return fmt.Errorf("create follow record: %w", err)
	}

	// Store in database
	follow := &domain.Follow{
		FollowerDID:  followerDID,
		FollowingDID: targetActor.DID,
		URI:          uri,
		CID:          &cid,
		CreatedAt:    record.CreatedAt,
	}

	rawData, _ := json.Marshal(record)
	follow.Raw = rawData

	return s.socialRepo.CreateFollow(ctx, follow)
}

// Unfollow removes a follow relationship
func (s *Service) Unfollow(ctx context.Context, followerDID, targetHandle string) error {
	targetActor, err := s.resolveActor(ctx, targetHandle)
	if err != nil {
		return fmt.Errorf("resolve target actor: %w", err)
	}

	// Get follow record URI
	follow, err := s.socialRepo.GetFollow(ctx, followerDID, targetActor.DID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("not following")
		}
		return err
	}

	followURI := follow.URI

	// Delete record from ATProto
	if err := s.deleteRecord(ctx, followURI); err != nil {
		return fmt.Errorf("delete follow record: %w", err)
	}

	// Mark as revoked in database
	return s.socialRepo.RevokeFollow(ctx, followURI)
}

// Like creates a like on a post/video
func (s *Service) Like(ctx context.Context, actorDID, subjectURI, subjectCID string) error {
	// Check if already liked
	hasLiked, _ := s.socialRepo.HasLiked(ctx, actorDID, subjectURI)
	if hasLiked {
		return fmt.Errorf("already liked")
	}

	// Create like record in ATProto
	record := LikeRecord{
		Type: "app.bsky.feed.like",
		Subject: Subject{
			URI: subjectURI,
			CID: subjectCID,
		},
		CreatedAt: time.Now().UTC(),
	}

	uri, cid, err := s.createRecord(ctx, actorDID, "app.bsky.feed.like", record)
	if err != nil {
		return fmt.Errorf("create like record: %w", err)
	}

	// Store in database
	like := &domain.Like{
		ActorDID:   actorDID,
		SubjectURI: subjectURI,
		SubjectCID: &subjectCID,
		URI:        uri,
		CID:        &cid,
		CreatedAt:  record.CreatedAt,
	}

	// Link to local video if applicable
	if strings.Contains(subjectURI, "/video/") {
		parts := strings.Split(subjectURI, "/video/")
		if len(parts) > 1 {
			videoID := strings.Split(parts[1], "/")[0]
			like.VideoID = &videoID
		}
	}

	rawData, _ := json.Marshal(record)
	like.Raw = rawData

	return s.socialRepo.CreateLike(ctx, like)
}

// Unlike removes a like from a post/video
func (s *Service) Unlike(ctx context.Context, actorDID, subjectURI string) error {
	// Get like record
	likes, err := s.socialRepo.GetLikes(ctx, subjectURI, 1000, 0)
	if err != nil {
		return err
	}

	var likeURI string
	for _, l := range likes {
		if l.ActorDID == actorDID {
			likeURI = l.URI
			break
		}
	}

	if likeURI == "" {
		return fmt.Errorf("not liked")
	}

	// Delete record from ATProto
	if err := s.deleteRecord(ctx, likeURI); err != nil {
		return fmt.Errorf("delete like record: %w", err)
	}

	// Remove from database
	return s.socialRepo.DeleteLike(ctx, likeURI)
}

// Comment creates a comment/reply on a post
func (s *Service) Comment(
	ctx context.Context,
	actorDID string,
	text string,
	rootURI, rootCID string,
	parentURI, parentCID string,
) (*domain.SocialComment, error) {
	// Create comment as a post with reply field
	record := CommentRecord{
		Type:      "app.bsky.feed.post",
		Text:      text,
		CreatedAt: time.Now().UTC(),
	}

	if rootURI != "" {
		record.Reply = &Reply{
			Root: Subject{
				URI: rootURI,
				CID: rootCID,
			},
			Parent: Subject{
				URI: parentURI,
				CID: parentCID,
			},
		}
		if parentURI == "" {
			record.Reply.Parent = record.Reply.Root
		}
	}

	uri, cid, err := s.createRecord(ctx, actorDID, "app.bsky.feed.post", record)
	if err != nil {
		return nil, fmt.Errorf("create comment record: %w", err)
	}

	// Get actor info
	actor, _ := s.socialRepo.GetActorByDID(ctx, actorDID)

	// Store in database
	comment := &domain.SocialComment{
		ActorDID:  actorDID,
		URI:       uri,
		CID:       &cid,
		Text:      text,
		RootURI:   rootURI,
		RootCID:   &rootCID,
		CreatedAt: record.CreatedAt,
		IndexedAt: time.Now(),
		Blocked:   false,
	}

	if actor != nil {
		comment.ActorHandle = &actor.Handle
	}

	if parentURI != "" {
		comment.ParentURI = &parentURI
		comment.ParentCID = &parentCID
	}

	// Link to local video if applicable
	if strings.Contains(rootURI, "/video/") {
		parts := strings.Split(rootURI, "/video/")
		if len(parts) > 1 {
			videoID := strings.Split(parts[1], "/")[0]
			comment.VideoID = &videoID
		}
	}

	rawData, _ := json.Marshal(record)
	comment.Raw = rawData

	if err := s.socialRepo.CreateComment(ctx, comment); err != nil {
		return nil, err
	}

	return comment, nil
}

// DeleteComment removes a comment
func (s *Service) DeleteComment(ctx context.Context, uri string) error {
	// Delete record from ATProto
	if err := s.deleteRecord(ctx, uri); err != nil {
		return fmt.Errorf("delete comment record: %w", err)
	}

	// Remove from database
	return s.socialRepo.DeleteComment(ctx, uri)
}

// ApplyModerationLabel applies a moderation label to an actor or content
func (s *Service) ApplyModerationLabel(
	ctx context.Context,
	actorDID string,
	labelType string,
	reason string,
	appliedBy string,
	uri string,
	expiresIn time.Duration,
) error {
	label := &domain.ModerationLabel{
		ActorDID:  actorDID,
		LabelType: labelType,
		AppliedBy: appliedBy,
		CreatedAt: time.Now(),
	}

	if reason != "" {
		label.Reason = &reason
	}

	if uri != "" {
		label.URI = &uri
	}

	if expiresIn > 0 {
		expiresAt := time.Now().Add(expiresIn)
		label.ExpiresAt = &expiresAt
	}

	// Create label in ATProto (if integrated with labeler service)
	if s.cfg.EnableATProtoLabeler {
		if err := s.createLabel(ctx, label); err != nil {
			return err
		}
	}

	return s.socialRepo.CreateModerationLabel(ctx, label)
}

// RemoveModerationLabel removes a moderation label
func (s *Service) RemoveModerationLabel(ctx context.Context, id string) error {
	return s.socialRepo.RemoveModerationLabel(ctx, id)
}

// GetModerationLabels retrieves moderation labels for an actor DID
func (s *Service) GetModerationLabels(ctx context.Context, did string) ([]domain.ModerationLabel, error) {
	return s.socialRepo.GetModerationLabels(ctx, did)
}

// GetSocialStats retrieves aggregate social stats for an actor
func (s *Service) GetSocialStats(ctx context.Context, handle string) (*domain.SocialStats, error) {
	actor, err := s.resolveActor(ctx, handle)
	if err != nil {
		return nil, err
	}
	return s.socialRepo.GetSocialStats(ctx, actor.DID)
}

// ResolveActor resolves an actor by handle
func (s *Service) ResolveActor(ctx context.Context, handle string) (*domain.ATProtoActor, error) {
	return s.resolveActor(ctx, handle)
}

// GetFollowers retrieves followers list
func (s *Service) GetFollowers(ctx context.Context, handle string, limit, offset int) ([]domain.Follow, error) {
	actor, err := s.resolveActor(ctx, handle)
	if err != nil {
		return nil, err
	}
	return s.socialRepo.GetFollowers(ctx, actor.DID, limit, offset)
}

// GetFollowing retrieves following list
func (s *Service) GetFollowing(ctx context.Context, handle string, limit, offset int) ([]domain.Follow, error) {
	actor, err := s.resolveActor(ctx, handle)
	if err != nil {
		return nil, err
	}
	return s.socialRepo.GetFollowing(ctx, actor.DID, limit, offset)
}

// GetLikes retrieves likes for a subject
func (s *Service) GetLikes(ctx context.Context, subjectURI string, limit, offset int) ([]domain.Like, error) {
	return s.socialRepo.GetLikes(ctx, subjectURI, limit, offset)
}

// GetComments retrieves comments for a subject
func (s *Service) GetComments(ctx context.Context, rootURI string, limit, offset int) ([]domain.SocialComment, error) {
	return s.socialRepo.GetComments(ctx, rootURI, limit, offset)
}

// GetCommentThread retrieves a comment thread
func (s *Service) GetCommentThread(ctx context.Context, parentURI string, limit, offset int) ([]domain.SocialComment, error) {
	return s.socialRepo.GetCommentThread(ctx, parentURI, limit, offset)
}

// IngestActorFeed pulls and stores an actor's feed (best-effort)
func (s *Service) IngestActorFeed(ctx context.Context, handle string, limit int) error {
	actor, err := s.resolveActor(ctx, handle)
	if err != nil {
		return err
	}
	blockedLabels, _ := s.socialRepo.GetBlockedLabels(ctx)
	feed, err := s.getActorFeed(ctx, actor.DID, limit)
	if err != nil {
		return err
	}
	// Process posts (simplified best-effort ingestion)
	if items, ok := feed["feed"].([]interface{}); ok {
		for _, it := range items {
			post := it.(map[string]interface{})
			if s.shouldBlock(post, blockedLabels) {
				continue
			}
			if reply, ok := post["reply"].(map[string]interface{}); ok {
				s.processReply(ctx, reply)
			}
			if uri, ok := post["uri"].(string); ok {
				if cid, ok := post["cid"].(string); ok {
					go s.ingestPostLikes(ctx, uri, cid)
				}
			}
		}
	}
	return nil
}

// resolveActor resolves handle to DID then fetches profile
func (s *Service) resolveActor(ctx context.Context, handle string) (*domain.ATProtoActor, error) {
	// Try local cache first
	if cached, err := s.socialRepo.GetActorByHandle(ctx, handle); err == nil && cached != nil {
		return cached, nil
	}

	// Resolve handle -> DID
	pds := strings.TrimRight(s.cfg.ATProtoPDSURL, "/")
	url := fmt.Sprintf("%s/xrpc/com.atproto.identity.resolveHandle?handle=%s", pds, handle)

	// SSRF Protection: Validate PDS URL before making request
	if err := s.urlValidator.ValidateURL(url); err != nil {
		return nil, fmt.Errorf("invalid or unsafe PDS URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("resolve handle failed: %d", resp.StatusCode)
	}

	var result struct {
		DID string `json:"did"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	// Get profile
	return s.getProfile(ctx, result.DID)
}

func (s *Service) getProfile(ctx context.Context, did string) (*domain.ATProtoActor, error) {
	pds := strings.TrimRight(s.cfg.ATProtoPDSURL, "/")
	url := fmt.Sprintf("%s/xrpc/app.bsky.actor.getProfile?actor=%s", pds, did)

	// SSRF Protection: Validate PDS URL before making request
	if err := s.urlValidator.ValidateURL(url); err != nil {
		return nil, fmt.Errorf("invalid or unsafe PDS URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get profile failed: %d", resp.StatusCode)
	}

	var profile map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, err
	}

	actor := &domain.ATProtoActor{
		DID:       did,
		Handle:    profile["handle"].(string),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		IndexedAt: time.Now(),
	}

	if name, ok := profile["displayName"].(string); ok {
		actor.DisplayName = &name
	}
	if bio, ok := profile["description"].(string); ok {
		actor.Bio = &bio
	}
	if avatar, ok := profile["avatar"].(string); ok {
		actor.AvatarURL = &avatar
	}
	if banner, ok := profile["banner"].(string); ok {
		actor.BannerURL = &banner
	}
	if labels, ok := profile["labels"]; ok {
		labelsJSON, _ := json.Marshal(labels)
		actor.Labels = labelsJSON
	}

	return actor, nil
}

func (s *Service) createRecord(ctx context.Context, repoDID, collection string, record interface{}) (string, string, error) {
	pds := strings.TrimRight(s.cfg.ATProtoPDSURL, "/")

	// Get session token
	accessToken, err := s.getAccessToken(ctx)
	if err != nil {
		return "", "", err
	}

	body := map[string]interface{}{
		"repo":       repoDID,
		"collection": collection,
		"record":     record,
		"validate":   true,
	}

	bodyData, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pds+"/xrpc/com.atproto.repo.createRecord", bytes.NewReader(bodyData))
	if err != nil {
		return "", "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("create record failed: %d - %s", resp.StatusCode, string(body))
	}

	var result struct {
		URI string `json:"uri"`
		CID string `json:"cid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", err
	}

	return result.URI, result.CID, nil
}

func (s *Service) deleteRecord(ctx context.Context, uri string) error {
	pds := strings.TrimRight(s.cfg.ATProtoPDSURL, "/")

	// Parse URI to get repo and rkey
	parts := strings.Split(uri, "/")
	if len(parts) < 5 {
		return fmt.Errorf("invalid URI format")
	}
	repo := parts[2]
	collection := parts[3]
	rkey := parts[4]

	// Get session token
	accessToken, err := s.getAccessToken(ctx)
	if err != nil {
		return err
	}

	body := map[string]interface{}{
		"repo":       repo,
		"collection": collection,
		"rkey":       rkey,
	}

	bodyData, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pds+"/xrpc/com.atproto.repo.deleteRecord", bytes.NewReader(bodyData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete record failed: %d - %s", resp.StatusCode, string(body))
	}

	return nil
}

func (s *Service) getActorFeed(ctx context.Context, did string, limit int) (map[string]interface{}, error) {
	pds := strings.TrimRight(s.cfg.ATProtoPDSURL, "/")
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	url := fmt.Sprintf("%s/xrpc/app.bsky.feed.getAuthorFeed?actor=%s&limit=%d", pds, did, limit)

	// SSRF Protection: Validate PDS URL before making request
	if err := s.urlValidator.ValidateURL(url); err != nil {
		return nil, fmt.Errorf("invalid or unsafe PDS URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get feed failed: %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result, nil
}

func (s *Service) getAccessToken(ctx context.Context) (string, error) {
	// This would integrate with the atproto service's session management
	// For now, returning a placeholder
	return s.cfg.ATProtoAppPassword, nil
}

func (s *Service) createLabel(ctx context.Context, label *domain.ModerationLabel) error {
	// This would integrate with ATProto labeler service
	// Implementation depends on labeler service setup
	return nil
}

func (s *Service) shouldBlock(post map[string]interface{}, blockedLabels []string) bool {
	if len(blockedLabels) == 0 {
		return false
	}

	labels, ok := post["labels"].([]interface{})
	if !ok {
		return false
	}

	for _, l := range labels {
		label := l.(map[string]interface{})
		if val, ok := label["val"].(string); ok {
			for _, blocked := range blockedLabels {
				if val == blocked {
					return true
				}
			}
		}
	}

	return false
}

func (s *Service) ingestPostLikes(ctx context.Context, uri, cid string) {
	// This would fetch and ingest likes for a post
	// Implementation depends on requirements
}

func (s *Service) processReply(ctx context.Context, post map[string]interface{}) {
	// This would process reply/comment threads
	// Implementation depends on requirements
}
