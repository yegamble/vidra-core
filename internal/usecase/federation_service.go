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
	repo    FederationRepository
	modRepo InstanceConfigReader
	atproto *atprotoService
	cfg     *config.Config
	rrIndex int // round-robin index for actors
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
	// Use concrete atprotoService if available to access helpers; otherwise wrap nil
	svc, _ := atproto.(*atprotoService)
	return &federationService{repo: repo, modRepo: modRepo, atproto: svc, cfg: cfg}
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
		if s.atproto == nil {
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
    if s.modRepo == nil { return nil }
    c, err := s.modRepo.GetInstanceConfig(ctx, "atproto_ingest_actors")
    if err != nil { return nil }
    var arr []string
    _ = json.Unmarshal(c.Value, &arr)
    out := make([]string, 0, len(arr))
    for _, a := range arr { if aa := strings.TrimSpace(a); aa != "" { out = append(out, aa) } }
    return out
}

func (s *federationService) ingestActor(ctx context.Context, actor string) error {
	if s.atproto == nil {
		return fmt.Errorf("atproto not configured")
	}

	// Get configured max items per actor per tick
	maxItems := s.cfg.FederationIngestMaxItems
	if maxItems <= 0 {
		maxItems = 40
	}

	// Get configured max pages to fetch in one tick
	maxPages := s.cfg.FederationIngestMaxPages
	if maxPages <= 0 {
		maxPages = 2
	}

	// Start with stored cursor
	cursor := s.getActorCursorTableAware(ctx, actor)
	totalIngested := 0
	pagesProcessed := 0

	// Process multiple pages up to configured limits
	for pagesProcessed < maxPages && totalIngested < maxItems {
		// Calculate limit for this page
		pageLimit := 20
		remaining := maxItems - totalIngested
		if remaining < pageLimit {
			pageLimit = remaining
		}

		feed, err := s.atproto.getAuthorFeed(ctx, actor, pageLimit, cursor)
		if err != nil {
			// On first page error, return error
			if pagesProcessed == 0 {
				return err
			}
			// On subsequent pages, break the loop but save progress
			break
		}

		items, _ := feed["feed"].([]any)
		if len(items) == 0 {
			// No more items, we're done
			break
		}

		// Get next cursor for potential next page
		nextCursor, hasNext := feed["cursor"].(string)
		hasNext = hasNext && strings.TrimSpace(nextCursor) != ""

		// Process items from this page (code continues below...)
		// Load blocked labels once (outside loop for efficiency)
		var blockedSet map[string]struct{}
		if pagesProcessed == 0 {
			var blocked []string
			if s.modRepo != nil {
				if c, err := s.modRepo.GetInstanceConfig(ctx, "atproto_block_labels"); err == nil {
					_ = json.Unmarshal(c.Value, &blocked)
				}
			}
			blockedSet = make(map[string]struct{})
			for _, b := range blocked {
				blockedSet[strings.ToLower(strings.TrimSpace(b))] = struct{}{}
			}
		}

		// Process each item in this page
		processedCount := 0
		for _, it := range items {
			m, _ := it.(map[string]any)
			post, _ := m["post"].(map[string]any)
			if post == nil {
				continue
			}
			uri, _ := post["uri"].(string)
			cid, _ := post["cid"].(string)
			rec, _ := post["record"].(map[string]any)
			if rec == nil {
				continue
			}
			text, _ := rec["text"].(string)
			createdAtStr, _ := rec["createdAt"].(string)
			var createdAt *time.Time
			if createdAtStr != "" {
				if t, err := time.Parse(time.RFC3339, createdAtStr); err == nil {
					createdAt = &t
				}
			}
			// Actor info
			author, _ := post["author"].(map[string]any)
			did, _ := author["did"].(string)
			handle, _ := author["handle"].(string)
			// Optional embed external
			var embedURL, embedTitle, embedDesc *string
			if emb, ok := rec["embed"].(map[string]any); ok {
				if embType, _ := emb["$type"].(string); embType == "app.bsky.embed.external" {
					if ext, ok := emb["external"].(map[string]any); ok {
						if u, ok := ext["uri"].(string); ok {
							embedURL = &u
						}
						if t, ok := ext["title"].(string); ok {
							embedTitle = &t
						}
						if d, ok := ext["description"].(string); ok {
							embedDesc = &d
						}
					}
				}
			}
			// Labels filtering (basic)
			var labelsRaw json.RawMessage
			if lab, ok := post["labels"].(map[string]any); ok {
				if bts, err := json.Marshal(lab); err == nil {
					labelsRaw = bts
				}
				// Attempt to read values array
				if vals, ok := lab["values"].([]any); ok {
					skip := false
					for _, vv := range vals {
						if mm, ok := vv.(map[string]any); ok {
							if val, _ := mm["val"].(string); val != "" {
								if _, bad := blockedSet[strings.ToLower(val)]; bad {
									skip = true
									break
								}
							}
						}
					}
					if skip {
						continue
					}
				}
			}
			rawBytes, _ := json.Marshal(m)
			// Build record
			p := &domain.FederatedPost{
				ActorDID:         did,
				URI:              uri,
				Text:             strPtrIf(text != "", text),
				CID:              strPtrIf(cid != "", cid),
				ActorHandle:      strPtrIf(handle != "", handle),
				CreatedAt:        createdAt,
				EmbedURL:         embedURL,
				EmbedTitle:       embedTitle,
				EmbedDescription: embedDesc,
				Labels:           labelsRaw,
				Raw:              rawBytes,
			}
			_ = s.repo.UpsertPost(ctx, p)
			processedCount++
		}

		totalIngested += processedCount
		pagesProcessed++

		// Update cursor for next iteration
		if hasNext {
			cursor = nextCursor
			// Save cursor after each page so we can resume
			_ = s.setActorCursorTableAware(ctx, actor, cursor)
		} else {
			// No more pages available
			break
		}

		// Check if we've hit our limits
		if totalIngested >= maxItems {
			break
		}
	}

	if totalIngested > 0 {
		metrics.AddFedPostsIngested(totalIngested)
	}

	return nil
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
        if c, _, _, _, err := s.repo.GetActorStateSimple(ctx, actor); err == nil { return c }
    }
    return s.getActorCursor(ctx, actor)
}

func (s *federationService) setActorCursorTableAware(ctx context.Context, actor string, cursor string) error {
    if s.repo != nil { _ = s.repo.SetActorCursor(ctx, actor, cursor) }
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
