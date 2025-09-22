package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/metrics"
)

// FederationService processes federation jobs and performs periodic ingestion.
type FederationService interface {
	ProcessNext(ctx context.Context) (bool, error)
}

type federationService struct {
	repo             FederationRepository
	modRepo          InstanceConfigReader
	atproto          *atprotoService
	atprotoPublisher AtprotoPublisher // Original publisher interface (for tests)
	cfg              *config.Config
	rrIndex          int // round-robin index for actors
}

// FederationRepository abstracts job queue and post storage used by the federation service.
type FederationRepository interface {
	EnqueueJob(ctx context.Context, jobType string, payload any, runAt time.Time) (string, error)
	GetNextJob(ctx context.Context) (*domain.FederationJob, error)
	CompleteJob(ctx context.Context, id string) error
	RescheduleJob(ctx context.Context, id string, lastErr string, backoff time.Duration) error
	UpsertPost(ctx context.Context, p *domain.FederatedPost) error
	// Actors
	ListEnabledActors(ctx context.Context) ([]string, error)
	GetActorStateSimple(ctx context.Context, actor string) (cursor string, nextAt *time.Time, attempts int, rateLimitSeconds int, err error)
	SetActorCursor(ctx context.Context, actor string, cursor string) error
	SetActorNextAt(ctx context.Context, actor string, t time.Time) error
	SetActorAttempts(ctx context.Context, actor string, n int) error
}

func NewFederationService(repo FederationRepository, modRepo InstanceConfigReader, atproto AtprotoPublisher, cfg *config.Config) FederationService {
	// Use concrete atprotoService if available to access helpers
	svc, ok := atproto.(*atprotoService)
	if !ok && atproto != nil {
		// If we have a non-nil AtprotoPublisher that's not *atprotoService (e.g., mock in tests),
		// wrap it in a minimal adapter
		svc = &atprotoService{
			enabled: cfg != nil && cfg.EnableATProto,
			cfg:     cfg,
		}
		// Store the original publisher for delegation
		// Note: For tests, we'll check if atproto is non-nil to indicate it's configured
	}
	return &federationService{repo: repo, modRepo: modRepo, atproto: svc, cfg: cfg, atprotoPublisher: atproto}
}

func (s *federationService) ProcessNext(ctx context.Context) (bool, error) {
	// First, process queued job if any
	if job, err := s.repo.GetNextJob(ctx); err != nil {
		return false, err
	} else if job != nil {
		err := s.processJob(ctx, job)
		if err != nil {
			// Exponential backoff: min(2^attempt * 10s, 10m)
			backoff := time.Duration(10*(1<<uint(job.Attempts))) * time.Second
			if backoff > 10*time.Minute {
				backoff = 10 * time.Minute
			}
			_ = s.repo.RescheduleJob(ctx, job.ID, err.Error(), backoff)
			metrics.IncFedJobsFailed()
		} else {
			_ = s.repo.CompleteJob(ctx, job.ID)
			metrics.IncFedJobsProcessed()
		}
		return true, err
	}

	// Otherwise, perform one ingestion tick (round‑robin over configured actors)
	if s.atproto == nil {
		return false, nil
	}
	actors := s.getIngestActors(ctx)
	if len(actors) == 0 {
		return false, nil
	}
	// find next ready actor based on per-actor next_at gate
	var actor string
	readyFound := false
	now := time.Now()
	for i := 0; i < len(actors); i++ {
		idx := (s.rrIndex + i) % len(actors)
		a := actors[idx]
		nextAt := s.getActorNextAt(ctx, a)
		if nextAt.IsZero() || !nextAt.After(now) {
			actor = a
			s.rrIndex = idx + 1
			readyFound = true
			break
		}
	}
	if !readyFound {
		return false, nil
	}
	if err := s.ingestActor(ctx, actor); err != nil {
		// Non-fatal: backoff per actor
		s.bumpActorBackoff(ctx, actor)
		return false, nil
	}
	// Success: reset backoff and schedule next run after ingest interval
	s.resetActorBackoff(ctx, actor)
	if s.repo != nil {
		_ = s.repo.SetActorNextAt(ctx, actor, now.Add(time.Duration(s.cfg.FederationIngestIntervalSeconds)*time.Second))
	} else {
		s.setActorNextAt(ctx, actor, now.Add(time.Duration(s.cfg.FederationIngestIntervalSeconds)*time.Second))
	}
	return true, nil
}

func (s *federationService) processJob(ctx context.Context, job *domain.FederationJob) error {
	switch job.JobType {
	case "publish_post":
		// Check if we have an ATProto publisher (either concrete or mock)
		if s.atprotoPublisher == nil && s.atproto == nil {
			return fmt.Errorf("atproto not configured")
		}
		var payload struct {
			VideoID string `json:"videoId"`
		}
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return err
		}
		// For publishing, we need a video instance; pull a minimal proxy video
		// FederationService doesn't have direct access to video repo; rely on inline publish path to embed URL only
		v := &domain.Video{ID: payload.VideoID, Title: "", Description: "", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted}

		// Use the original publisher if available (for tests), otherwise use atproto
		if s.atprotoPublisher != nil {
			return s.atprotoPublisher.PublishVideo(ctx, v)
		}
		return s.atproto.PublishVideo(ctx, v)
	default:
		return fmt.Errorf("unknown federation job type: %s", job.JobType)
	}
}

func (s *federationService) getIngestActors(ctx context.Context) []string {
	// Prefer dedicated table when available
	if s.repo != nil {
		if names, err := s.repo.ListEnabledActors(ctx); err == nil && len(names) > 0 {
			return names
		}
	}
	if s.modRepo == nil {
		return nil
	}
	c, err := s.modRepo.GetInstanceConfig(ctx, "atproto_ingest_actors")
	if err != nil {
		return nil
	}
	var arr []string
	_ = json.Unmarshal(c.Value, &arr)
	out := make([]string, 0, len(arr))
	for _, a := range arr {
		if aa := strings.TrimSpace(a); aa != "" {
			out = append(out, aa)
		}
	}
	return out
}

func (s *federationService) ingestActor(ctx context.Context, actor string) error {
	if s.atproto == nil {
		return fmt.Errorf("atproto not configured")
	}

	maxItems := s.getMaxItems()
	maxPages := s.getMaxPages()
	cursor := s.getActorCursorTableAware(ctx, actor)

	blockedSet := s.loadBlockedLabels(ctx)
	totalIngested := 0
	pagesProcessed := 0

	for pagesProcessed < maxPages && totalIngested < maxItems {
		pageLimit := s.calculatePageLimit(maxItems, totalIngested)

		feed, err := s.atproto.getAuthorFeed(ctx, actor, pageLimit, cursor)
		if err != nil {
			if pagesProcessed == 0 {
				return err
			}
			break
		}

		items, _ := feed["feed"].([]any)
		if len(items) == 0 {
			break
		}

		processedCount := s.processPageItems(ctx, items, blockedSet)
		totalIngested += processedCount
		pagesProcessed++

		// Update cursor
		if nextCursor, ok := feed["cursor"].(string); ok && strings.TrimSpace(nextCursor) != "" {
			cursor = nextCursor
			_ = s.setActorCursorTableAware(ctx, actor, cursor)
		} else {
			break
		}

		if totalIngested >= maxItems {
			break
		}
	}

	if totalIngested > 0 {
		metrics.AddFedPostsIngested(totalIngested)
	}
	return nil
}

func (s *federationService) getMaxItems() int {
	if s.cfg.FederationIngestMaxItems > 0 {
		return s.cfg.FederationIngestMaxItems
	}
	return 40
}

func (s *federationService) getMaxPages() int {
	if s.cfg.FederationIngestMaxPages > 0 {
		return s.cfg.FederationIngestMaxPages
	}
	return 2
}

func (s *federationService) calculatePageLimit(maxItems, totalIngested int) int {
	pageLimit := 20
	remaining := maxItems - totalIngested
	if remaining < pageLimit {
		return remaining
	}
	return pageLimit
}

func (s *federationService) loadBlockedLabels(ctx context.Context) map[string]struct{} {
	blockedSet := make(map[string]struct{})
	if s.modRepo == nil {
		return blockedSet
	}

	c, err := s.modRepo.GetInstanceConfig(ctx, "atproto_block_labels")
	if err != nil {
		return blockedSet
	}

	var blocked []string
	_ = json.Unmarshal(c.Value, &blocked)

	for _, b := range blocked {
		blockedSet[strings.ToLower(strings.TrimSpace(b))] = struct{}{}
	}
	return blockedSet
}

func (s *federationService) processPageItems(ctx context.Context, items []any, blockedSet map[string]struct{}) int {
	processedCount := 0
	for _, it := range items {
		if s.processItem(ctx, it, blockedSet) {
			processedCount++
		}
	}
	return processedCount
}

func (s *federationService) processItem(ctx context.Context, item any, blockedSet map[string]struct{}) bool {
	m, _ := item.(map[string]any)
	post, _ := m["post"].(map[string]any)
	if post == nil {
		return false
	}

	rec, _ := post["record"].(map[string]any)
	if rec == nil {
		return false
	}

	// Check blocked labels
	if s.hasBlockedLabel(post, blockedSet) {
		return false
	}

	p := s.buildFederatedPost(m, post, rec)
	if p == nil {
		return false
	}

	_ = s.repo.UpsertPost(ctx, p)
	return true
}

func (s *federationService) hasBlockedLabel(post map[string]any, blockedSet map[string]struct{}) bool {
	lab, ok := post["labels"].(map[string]any)
	if !ok {
		return false
	}

	vals, ok := lab["values"].([]any)
	if !ok {
		return false
	}

	for _, vv := range vals {
		mm, ok := vv.(map[string]any)
		if !ok {
			continue
		}
		val, _ := mm["val"].(string)
		if val != "" {
			if _, bad := blockedSet[strings.ToLower(val)]; bad {
				return true
			}
		}
	}
	return false
}

func (s *federationService) buildFederatedPost(m, post, rec map[string]any) *domain.FederatedPost {
	uri, _ := post["uri"].(string)
	cid, _ := post["cid"].(string)
	text, _ := rec["text"].(string)

	// Parse timestamp
	var createdAt *time.Time
	if createdAtStr, _ := rec["createdAt"].(string); createdAtStr != "" {
		if t, err := time.Parse(time.RFC3339, createdAtStr); err == nil {
			createdAt = &t
		}
	}

	// Actor info
	author, _ := post["author"].(map[string]any)
	did, _ := author["did"].(string)
	handle, _ := author["handle"].(string)

	// Extract embed
	embedType, embedURL, embedTitle, embedDesc := s.extractEmbedInfo(rec)

	// Labels
	var labelsRaw json.RawMessage
	if lab, ok := post["labels"].(map[string]any); ok {
		if bts, err := json.Marshal(lab); err == nil {
			labelsRaw = bts
		}
	}

	rawBytes, _ := json.Marshal(m)

	return &domain.FederatedPost{
		ActorDID:         did,
		URI:              uri,
		Text:             strPtrIf(text != "", text),
		CID:              strPtrIf(cid != "", cid),
		ActorHandle:      strPtrIf(handle != "", handle),
		CreatedAt:        createdAt,
		EmbedType:        embedType,
		EmbedURL:         embedURL,
		EmbedTitle:       embedTitle,
		EmbedDescription: embedDesc,
		Labels:           labelsRaw,
		Raw:              rawBytes,
	}
}

func (s *federationService) extractEmbedInfo(rec map[string]any) (*string, *string, *string, *string) {
	emb, ok := rec["embed"].(map[string]any)
	if !ok {
		return nil, nil, nil, nil
	}

	t, _ := emb["$type"].(string)
	// Normalize common embed types into a simple string we can persist
	// - external: app.bsky.embed.external
	// - images:   app.bsky.embed.images
	// - video:    app.bsky.embed.video, or recordWithMedia with media=video
	// - record:   app.bsky.embed.record
	var embedType *string
	switch t {
	case "app.bsky.embed.external":
		et := "external"
		embedType = &et
		// extract external details
		if ext, ok := emb["external"].(map[string]any); ok {
			var embedURL, embedTitle, embedDesc *string
			if u, ok := ext["uri"].(string); ok {
				embedURL = &u
			}
			if ti, ok := ext["title"].(string); ok {
				embedTitle = &ti
			}
			if d, ok := ext["description"].(string); ok {
				embedDesc = &d
			}
			return embedType, embedURL, embedTitle, embedDesc
		}
		return embedType, nil, nil, nil
	case "app.bsky.embed.images":
		et := "images"
		embedType = &et
		return embedType, nil, nil, nil
	case "app.bsky.embed.video":
		et := "video"
		embedType = &et
		return embedType, nil, nil, nil
	case "app.bsky.embed.recordWithMedia":
		// Inspect nested media type
		if media, ok := emb["media"].(map[string]any); ok {
			if mt, _ := media["$type"].(string); mt != "" {
				switch mt {
				case "app.bsky.embed.video":
					et := "video"
					embedType = &et
				case "app.bsky.embed.images":
					et := "images"
					embedType = &et
				default:
					// unknown media; leave unset
				}
			} else {
				// Heuristic: presence of 'video' key may imply a video embed
				if _, hasVideo := media["video"]; hasVideo {
					et := "video"
					embedType = &et
				} else if _, hasImages := media["images"]; hasImages {
					et := "images"
					embedType = &et
				}
			}
		}
		return embedType, nil, nil, nil
	case "app.bsky.embed.record":
		et := "record"
		embedType = &et
		return embedType, nil, nil, nil
	default:
		// Unknown embed type
		return nil, nil, nil, nil
	}
}

func strPtrIf(ok bool, v string) *string {
	if ok {
		return &v
	}
	return nil
}

// per-actor cursor persistence using instance_config as a simple store
func (s *federationService) getActorCursor(ctx context.Context, actor string) string {
	if s.modRepo == nil {
		return ""
	}
	key := "atproto_cursor_" + actor
	if cfg, err := s.modRepo.GetInstanceConfig(ctx, key); err == nil {
		var cur string
		_ = json.Unmarshal(cfg.Value, &cur)
		return cur
	}
	return ""
}

func (s *federationService) setActorCursor(ctx context.Context, actor string, cursor string) error {
	if s.modRepo == nil {
		return nil
	}
	key := "atproto_cursor_" + actor
	val, _ := json.Marshal(cursor)
	// store as private
	type updater interface {
		UpdateInstanceConfig(ctx context.Context, key string, value []byte, isPublic bool) error
	}
	if u, ok := s.modRepo.(updater); ok {
		return u.UpdateInstanceConfig(ctx, key, val, false)
	}
	return nil
}

// Table-aware helpers for cursor/nextAt
func (s *federationService) getActorCursorTableAware(ctx context.Context, actor string) string {
	if s.repo != nil {
		if c, _, _, _, err := s.repo.GetActorStateSimple(ctx, actor); err == nil {
			return c
		}
	}
	return s.getActorCursor(ctx, actor)
}

func (s *federationService) setActorCursorTableAware(ctx context.Context, actor string, cursor string) error {
	if s.repo != nil {
		_ = s.repo.SetActorCursor(ctx, actor, cursor)
	}
	return s.setActorCursor(ctx, actor, cursor)
}

// backoff helpers stored in instance_config keys
func (s *federationService) getActorNextAt(ctx context.Context, actor string) time.Time {
	if s.modRepo == nil {
		return time.Time{}
	}
	key := "atproto_actor_" + actor + "_next_at"
	if cfg, err := s.modRepo.GetInstanceConfig(ctx, key); err == nil {
		var ts string
		if err := json.Unmarshal(cfg.Value, &ts); err == nil {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

func (s *federationService) setActorNextAt(ctx context.Context, actor string, t time.Time) {
	if s.modRepo == nil {
		return
	}
	key := "atproto_actor_" + actor + "_next_at"
	b, _ := json.Marshal(t.UTC().Format(time.RFC3339))
	type updater interface {
		UpdateInstanceConfig(ctx context.Context, key string, value []byte, isPublic bool) error
	}
	if u, ok := s.modRepo.(updater); ok {
		_ = u.UpdateInstanceConfig(ctx, key, b, false)
	}
}

func (s *federationService) bumpActorBackoff(ctx context.Context, actor string) {
	if s.modRepo == nil {
		return
	}
	// attempts
	attempts := s.getActorAttempts(ctx, actor) + 1
	s.setActorAttempts(ctx, actor, attempts)
	// compute next time with capped exponential backoff
	backoff := time.Duration(10*(1<<uint(attempts))) * time.Second
	if backoff > 10*time.Minute {
		backoff = 10 * time.Minute
	}
	s.setActorNextAt(ctx, actor, time.Now().Add(backoff))
}

func (s *federationService) resetActorBackoff(ctx context.Context, actor string) {
	s.setActorAttempts(ctx, actor, 0)
}

func (s *federationService) getActorAttempts(ctx context.Context, actor string) int {
	if s.modRepo == nil {
		return 0
	}
	key := "atproto_actor_" + actor + "_attempts"
	if cfg, err := s.modRepo.GetInstanceConfig(ctx, key); err == nil {
		var n int
		_ = json.Unmarshal(cfg.Value, &n)
		return n
	}
	return 0
}

func (s *federationService) setActorAttempts(ctx context.Context, actor string, n int) {
	if s.modRepo == nil {
		return
	}
	key := "atproto_actor_" + actor + "_attempts"
	b, _ := json.Marshal(n)
	type updater interface {
		UpdateInstanceConfig(ctx context.Context, key string, value []byte, isPublic bool) error
	}
	if u, ok := s.modRepo.(updater); ok {
		_ = u.UpdateInstanceConfig(ctx, key, b, false)
	}
}
