// Package account provides PeerTube-compatible /accounts/{name} HTTP handlers.
// These routes resolve a username (or @user@domain handle) to a user profile.
package account

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/usecase"
	ucchannel "athena/internal/usecase/channel"
)

// AccountHandlers handles PeerTube-compatible /accounts/{name}/* routes.
type AccountHandlers struct {
	userRepo       usecase.UserRepository
	videoRepo      usecase.VideoRepository
	channelService *ucchannel.Service
	subRepo        usecase.SubscriptionRepository
}

// NewAccountHandlers constructs AccountHandlers.
// videoRepo, channelService, and subRepo may be nil; their endpoints will
// return empty lists rather than panicking.
func NewAccountHandlers(
	userRepo usecase.UserRepository,
	videoRepo usecase.VideoRepository,
	channelService *ucchannel.Service,
	subRepo usecase.SubscriptionRepository,
) *AccountHandlers {
	return &AccountHandlers{
		userRepo:       userRepo,
		videoRepo:      videoRepo,
		channelService: channelService,
		subRepo:        subRepo,
	}
}

// resolveHandle strips the leading '@' and optional '@domain' suffix from a
// PeerTube-style handle, returning the bare username.
//
//	"alice"              → "alice"
//	"@alice"             → "alice"
//	"@alice@example.com" → "alice"
func resolveHandle(name string) string {
	name = strings.TrimPrefix(name, "@")
	if idx := strings.IndexByte(name, '@'); idx >= 0 {
		name = name[:idx]
	}
	return name
}

// accountResponse is the public-safe account representation returned by these endpoints.
type accountResponse struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

func toAccountResponse(u *domain.User) accountResponse {
	return accountResponse{
		ID:          u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Description: u.Bio,
		CreatedAt:   u.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

// lookupUser resolves a handle-or-username URL param to a domain.User.
// On failure it writes the appropriate HTTP error and returns nil.
func (h *AccountHandlers) lookupUser(w http.ResponseWriter, r *http.Request, name string) *domain.User {
	username := resolveHandle(name)
	user, err := h.userRepo.GetByUsername(r.Context(), username)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("ACCOUNT_NOT_FOUND", "Account not found"))
			return nil
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to load account"))
		return nil
	}
	return user
}

// ListAccounts handles GET /api/v1/accounts
func (h *AccountHandlers) ListAccounts(w http.ResponseWriter, r *http.Request) {
	page, pageSize := parsePagination(r)
	offset := (page - 1) * pageSize

	users, err := h.userRepo.List(r.Context(), pageSize, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list accounts"))
		return
	}

	total, err := h.userRepo.Count(r.Context())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to count accounts"))
		return
	}

	data := make([]accountResponse, len(users))
	for i, u := range users {
		data[i] = toAccountResponse(u)
	}
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"total": total,
		"data":  data,
	})
}

// GetAccount handles GET /api/v1/accounts/{name}
func (h *AccountHandlers) GetAccount(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	user := h.lookupUser(w, r, name)
	if user == nil {
		return
	}
	shared.WriteJSON(w, http.StatusOK, toAccountResponse(user))
}

// GetAccountVideos handles GET /api/v1/accounts/{name}/videos
func (h *AccountHandlers) GetAccountVideos(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	user := h.lookupUser(w, r, name)
	if user == nil {
		return
	}

	if h.videoRepo == nil {
		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"total": 0, "data": []interface{}{}})
		return
	}

	page, pageSize := parsePagination(r)
	offset := (page - 1) * pageSize

	videos, total, err := h.videoRepo.GetByUserID(r.Context(), user.ID, pageSize, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get videos"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"total": total,
		"data":  videos,
	})
}

// GetAccountVideoChannels handles GET /api/v1/accounts/{name}/video-channels
func (h *AccountHandlers) GetAccountVideoChannels(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	user := h.lookupUser(w, r, name)
	if user == nil {
		return
	}

	if h.channelService == nil {
		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"total": 0, "data": []interface{}{}})
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Invalid user ID"))
		return
	}

	channels, err := h.channelService.GetUserChannels(r.Context(), userID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get channels"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"total": len(channels),
		"data":  channels,
	})
}

// GetAccountRatings handles GET /api/v1/accounts/{name}/ratings
func (h *AccountHandlers) GetAccountRatings(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	user := h.lookupUser(w, r, name)
	if user == nil {
		return
	}
	// Ratings are not publicly exposed per-account in this implementation.
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"total": 0, "data": []interface{}{}})
}

// GetAccountFollowers handles GET /api/v1/accounts/{name}/followers
func (h *AccountHandlers) GetAccountFollowers(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	user := h.lookupUser(w, r, name)
	if user == nil {
		return
	}
	// Follower lists are managed through ActivityPub and not publicly exposed here.
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"total": 0, "data": []interface{}{}})
}

// parsePagination returns page and pageSize from PeerTube-style query params
// (start= offset, count= page size) with sane defaults.
func parsePagination(r *http.Request) (page, pageSize int) {
	page = 1
	pageSize = 15
	if s := r.URL.Query().Get("count"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 && v <= 100 {
			pageSize = v
		}
	}
	if s := r.URL.Query().Get("start"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v >= 0 {
			page = v/pageSize + 1
		}
	}
	return
}
