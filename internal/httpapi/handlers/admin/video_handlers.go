package admin

import (
	"net/http"
	"strconv"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/usecase"
)

// AdminVideoHandlers handles admin video management HTTP requests.
type AdminVideoHandlers struct {
	videoRepo usecase.VideoRepository
}

// NewAdminVideoHandlers creates a new AdminVideoHandlers.
func NewAdminVideoHandlers(videoRepo usecase.VideoRepository) *AdminVideoHandlers {
	return &AdminVideoHandlers{videoRepo: videoRepo}
}

// ListVideos handles GET /api/v1/admin/videos
// Supports ?limit=20&offset=0&search=term&sort=created_at query params.
func (h *AdminVideoHandlers) ListVideos(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	search := r.URL.Query().Get("search")
	sort := r.URL.Query().Get("sort")
	if sort == "" {
		sort = "created_at"
	}

	req := &domain.VideoSearchRequest{
		Query:  search,
		Limit:  limit,
		Offset: offset,
		Sort:   sort,
		Order:  "desc",
	}

	videos, total, err := h.videoRepo.List(r.Context(), req)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list videos"))
		return
	}

	shared.WriteJSONWithMeta(w, http.StatusOK, videos, &shared.Meta{
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}
