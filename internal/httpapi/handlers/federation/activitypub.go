package federation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/port"
)

// ActivityPubHandlers handles ActivityPub protocol endpoints
type ActivityPubHandlers struct {
	service   port.ActivityPubService
	cfg       *config.Config
	userRepo  port.UserRepository
	videoRepo port.VideoRepository
}

// NewActivityPubHandlers creates a new ActivityPub handlers instance
func NewActivityPubHandlers(service port.ActivityPubService, cfg *config.Config, userRepo port.UserRepository, videoRepo port.VideoRepository) *ActivityPubHandlers {
	return &ActivityPubHandlers{
		service:   service,
		cfg:       cfg,
		userRepo:  userRepo,
		videoRepo: videoRepo,
	}
}

// WebFinger handles /.well-known/webfinger requests
func (h *ActivityPubHandlers) WebFinger(w http.ResponseWriter, r *http.Request) {
	resource := r.URL.Query().Get("resource")
	if resource == "" {
		http.Error(w, "missing resource parameter", http.StatusBadRequest)
		return
	}

	// Parse resource (acct:username@domain or https://domain/users/username)
	var username string
	if strings.HasPrefix(resource, "acct:") {
		// acct:username@domain
		acct := strings.TrimPrefix(resource, "acct:")
		parts := strings.Split(acct, "@")
		if len(parts) != 2 {
			http.Error(w, "invalid resource format", http.StatusBadRequest)
			return
		}
		username = parts[0]
	} else if strings.HasPrefix(resource, "http://") || strings.HasPrefix(resource, "https://") {
		// Extract username from URL
		parts := strings.Split(strings.TrimSuffix(resource, "/"), "/")
		if len(parts) >= 2 && parts[len(parts)-2] == "users" {
			username = parts[len(parts)-1]
		} else {
			http.Error(w, "invalid resource format", http.StatusBadRequest)
			return
		}
	} else {
		http.Error(w, "unsupported resource format", http.StatusBadRequest)
		return
	}

	// Build WebFinger response
	actorURL := fmt.Sprintf("%s/users/%s", h.cfg.PublicBaseURL, username)

	response := domain.WebFingerResponse{
		Subject: fmt.Sprintf("acct:%s@%s", username, h.cfg.ActivityPubDomain),
		Aliases: []string{actorURL},
		Links: []domain.WebFingerLink{
			{
				Rel:  "self",
				Type: "application/activity+json",
				Href: actorURL,
			},
			{
				Rel:  "http://webfinger.net/rel/profile-page",
				Type: "text/html",
				Href: actorURL,
			},
		},
	}

	w.Header().Set("Content-Type", "application/jrd+json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(response)
}

// NodeInfo handles /.well-known/nodeinfo requests
func (h *ActivityPubHandlers) NodeInfo(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"links": []map[string]string{
			{
				"rel":  "http://nodeinfo.diaspora.software/ns/schema/2.0",
				"href": h.cfg.PublicBaseURL + "/nodeinfo/2.0",
			},
		},
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(response)
}

// NodeInfo20 handles /nodeinfo/2.0 requests
func (h *ActivityPubHandlers) NodeInfo20(w http.ResponseWriter, r *http.Request) {
	// Fetch real statistics from the database
	var userCount int64
	if h.userRepo != nil {
		var err error
		userCount, err = h.userRepo.Count(r.Context())
		if err != nil {
			// Log error but don't fail the request, use 0 as fallback
			userCount = 0
		}
	}

	// Count local videos as posts
	var videoCount int64
	if h.videoRepo != nil {
		var err error
		videoCount, err = h.videoRepo.Count(r.Context())
		if err != nil {
			videoCount = 0
		}
	}

	nodeInfo := domain.NodeInfo{
		Version: "2.0",
		Software: domain.NodeInfoSoftware{
			Name:       "athena",
			Version:    "1.0.0",
			Repository: "https://github.com/yourusername/athena",
		},
		Protocols:         []string{"activitypub"},
		Services:          domain.NodeInfoServices{Inbound: []string{}, Outbound: []string{}},
		OpenRegistrations: true,
		Usage: domain.NodeInfoUsage{
			Users: domain.NodeInfoUsers{
				Total: int(userCount),
			},
			LocalPosts: int(videoCount),
		},
		Metadata: map[string]interface{}{
			"nodeName":        "Athena",
			"nodeDescription": h.cfg.ActivityPubInstanceDescription,
		},
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(nodeInfo)
}

// GetActor handles GET /users/:username (ActivityPub actor)
func (h *ActivityPubHandlers) GetActor(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	if username == "" {
		http.Error(w, "missing username", http.StatusBadRequest)
		return
	}

	actor, err := h.service.GetLocalActor(r.Context(), username)
	if err != nil {
		http.Error(w, "actor not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/activity+json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(actor)
}

// getOrderedCollectionHandler is a helper to handle ordered collection endpoints (outbox, followers, following)
func (h *ActivityPubHandlers) getOrderedCollectionHandler(
	w http.ResponseWriter, r *http.Request,
	collectionType string,
	fetchPage func(ctx context.Context, username string, page, limit int) (*domain.OrderedCollectionPage, error),
	fetchCount func(ctx context.Context, username string) (int, error),
) {
	username := chi.URLParam(r, "username")
	if username == "" {
		http.Error(w, "missing username", http.StatusBadRequest)
		return
	}

	pageStr := r.URL.Query().Get("page")
	if pageStr == "" {
		// Return collection overview
		actorURL := fmt.Sprintf("%s/users/%s", h.cfg.PublicBaseURL, username)
		collectionURL := actorURL + "/" + collectionType

		count, err := fetchCount(r.Context(), username)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to get %s count: %v", collectionType, err), http.StatusNotFound)
			return
		}

		collection := domain.OrderedCollection{
			Context:    domain.ActivityStreamsContext,
			Type:       domain.ObjectTypeOrderedCollection,
			ID:         collectionURL,
			TotalItems: count,
			First:      collectionURL + "?page=0",
		}

		w.Header().Set("Content-Type", "application/activity+json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(collection)
		return
	}

	// Return paginated collection
	page, _ := strconv.Atoi(pageStr)
	limit := h.cfg.ActivityPubMaxActivitiesPerPage

	collectionPage, err := fetchPage(r.Context(), username, page, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get %s", collectionType), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/activity+json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(collectionPage)
}

// GetOutbox handles GET /users/:username/outbox
func (h *ActivityPubHandlers) GetOutbox(w http.ResponseWriter, r *http.Request) {
	h.getOrderedCollectionHandler(w, r, "outbox", h.service.GetOutbox, h.service.GetOutboxCount)
}

// GetInbox handles GET /users/:username/inbox
func (h *ActivityPubHandlers) GetInbox(w http.ResponseWriter, r *http.Request) {
	// Inbox GET is typically not implemented or returns empty for privacy
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// PostInbox handles POST /users/:username/inbox
func (h *ActivityPubHandlers) PostInbox(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	if username == "" {
		http.Error(w, "missing username", http.StatusBadRequest)
		return
	}

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer func() { _ = r.Body.Close() }()

	// Parse activity
	var activity map[string]interface{}
	if err := json.Unmarshal(body, &activity); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Handle the activity
	if err := h.service.HandleInboxActivity(r.Context(), activity, r); err != nil {
		http.Error(w, fmt.Sprintf("failed to process activity: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// PostSharedInbox handles POST /inbox (shared inbox)
func (h *ActivityPubHandlers) PostSharedInbox(w http.ResponseWriter, r *http.Request) {
	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer func() { _ = r.Body.Close() }()

	// Parse activity
	var activity map[string]interface{}
	if err := json.Unmarshal(body, &activity); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Handle the activity (same as PostInbox but for shared inbox)
	if err := h.service.HandleInboxActivity(r.Context(), activity, r); err != nil {
		http.Error(w, fmt.Sprintf("failed to process activity: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// GetFollowers handles GET /users/:username/followers
func (h *ActivityPubHandlers) GetFollowers(w http.ResponseWriter, r *http.Request) {
	h.getOrderedCollectionHandler(w, r, "followers", h.service.GetFollowers, h.service.GetFollowersCount)
}

// GetFollowing handles GET /users/:username/following
func (h *ActivityPubHandlers) GetFollowing(w http.ResponseWriter, r *http.Request) {
	h.getOrderedCollectionHandler(w, r, "following", h.service.GetFollowing, h.service.GetFollowingCount)
}

// HostMeta handles /.well-known/host-meta requests
func (h *ActivityPubHandlers) HostMeta(w http.ResponseWriter, r *http.Request) {
	xml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0">
  <Link rel="lrdd" template="%s/.well-known/webfinger?resource={uri}"/>
</XRD>`, h.cfg.PublicBaseURL)

	w.Header().Set("Content-Type", "application/xrd+xml; charset=utf-8")
	_, _ = w.Write([]byte(xml))
}
