package federation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/usecase"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// VideoStore is the minimal video-repo surface the handlers need. Implemented at wire-time
// by an adapter over repository.videoRepository.
type VideoStore interface {
	Get(ctx context.Context, id string) (*domain.Video, error)
	SetAtprotoURI(ctx context.Context, videoID string, uri *string, ownerUserID string) error
}

// CommentStore reads existing atproto_comments rows for the video's federated replies.
// Implemented by an adapter; nil when the optional Phase 9-era table isn't desired.
type CommentStore interface {
	GetByVideoAtprotoURI(ctx context.Context, atprotoURI string) ([]AtprotoComment, error)
}

// AtprotoComment is the wire shape returned by /interactions. Mirrors the frontend's
// AtprotoInteractions reply schema.
type AtprotoComment struct {
	URI       string    `json:"uri"`
	Text      string    `json:"text"`
	AuthorDID string    `json:"author_did"`
	AuthorHandle string `json:"author_handle"`
	CreatedAt time.Time `json:"created_at"`
}

// AtprotoHandlers groups all 8 federation/atproto routes.
type AtprotoHandlers struct {
	svc       *usecase.UserAtprotoService
	videos    VideoStore
	comments  CommentStore
	publicURL func(video *domain.Video) string

	feedCache   *lruCache
	threadCache *lruCache
}

// NewAtprotoHandlers builds the handlers. publicURL renders the public watch URL for a
// video (used in the syndicated post text). comments may be nil; in that case Task 9 falls
// back to PDS getPostThread directly.
func NewAtprotoHandlers(svc *usecase.UserAtprotoService, videos VideoStore, comments CommentStore, publicURL func(*domain.Video) string) *AtprotoHandlers {
	return &AtprotoHandlers{
		svc:         svc,
		videos:      videos,
		comments:    comments,
		publicURL:   publicURL,
		feedCache:   newLRUCache(1000, 5*time.Minute),
		threadCache: newLRUCache(1000, 5*time.Minute),
	}
}

// RegisterRoutes mounts all 8 routes on the given router. /feed and /interactions are
// public; the rest are auth-gated by the caller (we apply middleware.Auth inside).
func (h *AtprotoHandlers) RegisterRoutes(r chi.Router, jwtSecret string) {
	r.Route("/federation/atproto", func(r chi.Router) {
		// Public reads — no auth.
		r.Get("/feed/{did}", h.GetFeed)
		r.Get("/interactions/{videoId}", h.GetInteractions)

		// Auth-gated.
		r.Group(func(r chi.Router) {
			r.Use(middleware.Auth(jwtSecret))
			r.Post("/connect", h.Connect)
			r.Delete("/disconnect", h.Disconnect)
			r.Get("/account", h.GetAccount)
			r.Get("/status", h.GetStatus)
			r.Post("/syndicate/{videoId}", h.Syndicate)
			r.Delete("/syndicate/{videoId}", h.Unsyndicate)
		})
	})
}

// ─── Connect ───

// ConnectRequest is the body for POST /connect. The app_password field is intentionally a
// json tag — it is NEVER logged.
type ConnectRequest struct {
	Handle      string `json:"handle"`
	AppPassword string `json:"app_password"`
	PDSURL      string `json:"pds_url,omitempty"`
}

// ConnectResponse returns the linked identity (no tokens — they're encrypted at rest).
type ConnectResponse struct {
	DID    string `json:"did"`
	Handle string `json:"handle"`
	PDSURL string `json:"pds_url"`
}

func (h *AtprotoHandlers) Connect(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}
	var req ConnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	// NOTE: do NOT log req — it carries the app-password.
	acct, err := h.svc.LinkAccount(r.Context(), userID.String(), req.Handle, req.AppPassword, req.PDSURL)
	if err != nil {
		// Map domain errors to HTTP. Body intentionally generic — never echo the password.
		if errors.Is(err, usecase.ErrInvalidCredentials) {
			shared.WriteError(w, http.StatusUnauthorized, errors.New("invalid handle or app password"))
			return
		}
		// Repository ErrDIDAlreadyLinked travels by string match; keep this conservative.
		if err.Error() == "atproto: DID already linked to another user" {
			shared.WriteError(w, http.StatusConflict, errors.New("this Bluesky identity is already linked to another Vidra account"))
			return
		}
		shared.WriteError(w, http.StatusBadGateway, errors.New("failed to link account"))
		return
	}
	shared.WriteJSON(w, http.StatusOK, ConnectResponse{
		DID: acct.DID, Handle: acct.Handle, PDSURL: acct.PDSURL,
	})
}

// ─── Disconnect ───

func (h *AtprotoHandlers) Disconnect(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}
	if err := h.svc.Disconnect(r.Context(), userID.String()); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to disconnect account"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Account ───

type AccountResponse struct {
	DID    string `json:"did"`
	Handle string `json:"handle"`
	PDSURL string `json:"pds_url"`
}

func (h *AtprotoHandlers) GetAccount(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}
	acct, err := h.svc.GetAccount(r.Context(), userID.String())
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			shared.WriteError(w, http.StatusNotFound, errors.New("no linked atproto account"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to load account"))
		return
	}
	shared.WriteJSON(w, http.StatusOK, AccountResponse{
		DID: acct.DID, Handle: acct.Handle, PDSURL: acct.PDSURL,
	})
}

// ─── Status ───

type StatusResponse struct {
	Enabled       bool   `json:"enabled"`
	UserLinked    bool   `json:"user_linked"`
	InstanceDID   string `json:"instance_did,omitempty"`
}

func (h *AtprotoHandlers) GetStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	resp := StatusResponse{Enabled: true}
	if ok {
		_, err := h.svc.GetAccount(r.Context(), userID.String())
		resp.UserLinked = err == nil
	}
	// instance_did surfacing intentionally deferred — not all handlers have moderation_repo.
	shared.WriteJSON(w, http.StatusOK, resp)
}

// ─── Syndicate ───

type SyndicateResponse struct {
	AtprotoURI string `json:"atproto_uri"`
}

func (h *AtprotoHandlers) Syndicate(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}
	videoID := chi.URLParam(r, "videoId")
	if _, err := uuid.Parse(videoID); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid video id"))
		return
	}
	video, err := h.videos.Get(r.Context(), videoID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, errors.New("video not found"))
		return
	}
	if video.UserID != userID.String() {
		shared.WriteError(w, http.StatusForbidden, errors.New("not the owner of this video"))
		return
	}
	if video.AtprotoURI != nil && *video.AtprotoURI != "" {
		shared.WriteError(w, http.StatusConflict, errors.New("video already syndicated; unsyndicate first"))
		return
	}

	publicURL := ""
	if h.publicURL != nil {
		publicURL = h.publicURL(video)
	}
	atURI, err := h.svc.PublishVideo(r.Context(), userID.String(), video, publicURL)
	if err != nil {
		if errors.Is(err, usecase.ErrSessionExpired) {
			shared.WriteError(w, http.StatusUnauthorized, errors.New("atproto session expired; re-link in settings"))
			return
		}
		shared.WriteError(w, http.StatusBadGateway, errors.New("failed to syndicate to atproto"))
		return
	}
	if err := h.videos.SetAtprotoURI(r.Context(), videoID, &atURI, userID.String()); err != nil {
		// Best-effort rollback of the Bluesky post.
		_ = h.svc.Unsyndicate(r.Context(), userID.String(), atURI)
		shared.WriteError(w, http.StatusInternalServerError, errors.New("syndicated but failed to persist"))
		return
	}
	shared.WriteJSON(w, http.StatusOK, SyndicateResponse{AtprotoURI: atURI})
}

func (h *AtprotoHandlers) Unsyndicate(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}
	videoID := chi.URLParam(r, "videoId")
	if _, err := uuid.Parse(videoID); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid video id"))
		return
	}
	video, err := h.videos.Get(r.Context(), videoID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, errors.New("video not found"))
		return
	}
	if video.UserID != userID.String() {
		shared.WriteError(w, http.StatusForbidden, errors.New("not the owner of this video"))
		return
	}
	if video.AtprotoURI == nil || *video.AtprotoURI == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	atURI := *video.AtprotoURI
	// Best-effort delete on PDS, but always clear locally.
	_ = h.svc.Unsyndicate(r.Context(), userID.String(), atURI)
	if err := h.videos.SetAtprotoURI(r.Context(), videoID, nil, userID.String()); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to clear atproto_uri"))
		return
	}
	// Invalidate cached interactions for this AT URI.
	h.threadCache.delete(atURI)
	w.WriteHeader(http.StatusNoContent)
}

// ─── Feed ───

func (h *AtprotoHandlers) GetFeed(w http.ResponseWriter, r *http.Request) {
	did := chi.URLParam(r, "did")
	if did == "" {
		shared.WriteError(w, http.StatusBadRequest, errors.New("did required"))
		return
	}
	count := 20
	cursor := r.URL.Query().Get("cursor")
	cacheKey := did + "|" + cursor

	if cached, ok := h.feedCache.get(cacheKey); ok {
		shared.WriteJSON(w, http.StatusOK, cached)
		return
	}
	// Public feed proxy via the PDS app.bsky.feed.getAuthorFeed XRPC. The user-facing
	// service handles refresh/auth for owner-scoped feeds; for public read we only need a
	// thin XRPC fetch. Implementation deferred to the service helper.
	posts, nextCursor, err := h.svc.PublicGetAuthorFeed(r.Context(), did, count, cursor)
	if err != nil {
		shared.WriteError(w, http.StatusBadGateway, errors.New("failed to fetch feed"))
		return
	}
	resp := map[string]any{
		"data":   posts,
		"cursor": nextCursor,
		"total":  len(posts),
	}
	h.feedCache.set(cacheKey, resp)
	shared.WriteJSON(w, http.StatusOK, resp)
}

// ─── Interactions ───

func (h *AtprotoHandlers) GetInteractions(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "videoId")
	if _, err := uuid.Parse(videoID); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid video id"))
		return
	}
	video, err := h.videos.Get(r.Context(), videoID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, errors.New("video not found"))
		return
	}
	if video.AtprotoURI == nil || *video.AtprotoURI == "" {
		shared.WriteError(w, http.StatusNotFound, errors.New("video has no atproto post"))
		return
	}
	atURI := *video.AtprotoURI
	if cached, ok := h.threadCache.get(atURI); ok {
		shared.WriteJSON(w, http.StatusOK, cached)
		return
	}

	// Tier 1: read from local atproto_comments table (populated by Phase 9 federation).
	var replies []AtprotoComment
	if h.comments != nil {
		replies, _ = h.comments.GetByVideoAtprotoURI(r.Context(), atURI)
	}
	// Tier 2: if nothing locally, fall back to live PDS getPostThread.
	if len(replies) == 0 {
		fetched, err := h.svc.PublicGetPostThread(r.Context(), atURI)
		if err == nil {
			replies = make([]AtprotoComment, 0, len(fetched))
			for _, r := range fetched {
				replies = append(replies, AtprotoComment{
					URI: r.URI, Text: r.Text,
					AuthorDID: r.AuthorDID, AuthorHandle: r.AuthorHandle,
					CreatedAt: r.CreatedAt,
				})
			}
		}
	}
	resp := map[string]any{
		"atproto_uri": atURI,
		"replies":     replies,
		"count":       len(replies),
	}
	h.threadCache.set(atURI, resp)
	shared.WriteJSON(w, http.StatusOK, resp)
}

// ─── Tiny LRU ───

type lruCache struct {
	mu   sync.Mutex
	cap  int
	ttl  time.Duration
	data map[string]lruEntry
}

type lruEntry struct {
	value    any
	expireAt time.Time
}

func newLRUCache(cap int, ttl time.Duration) *lruCache {
	return &lruCache{cap: cap, ttl: ttl, data: make(map[string]lruEntry)}
}

func (c *lruCache) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.data[key]
	if !ok || time.Now().After(e.expireAt) {
		return nil, false
	}
	return e.value, true
}

func (c *lruCache) set(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.data) >= c.cap {
		// naive eviction: drop ~10% of oldest entries
		drop := c.cap / 10
		if drop < 1 {
			drop = 1
		}
		i := 0
		for k := range c.data {
			if i >= drop {
				break
			}
			delete(c.data, k)
			i++
		}
	}
	c.data[key] = lruEntry{value: value, expireAt: time.Now().Add(c.ttl)}
}

func (c *lruCache) delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
}
