package video

import (
	"context"
	"net/http"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
)

// OverviewVideoRepository is the subset of video repo needed for overview queries.
type OverviewVideoRepository interface {
	List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error)
}

// OverviewHandlers handles video overview endpoints.
type OverviewHandlers struct {
	videoRepo OverviewVideoRepository
}

// NewOverviewHandlers creates a new OverviewHandlers.
func NewOverviewHandlers(videoRepo OverviewVideoRepository) *OverviewHandlers {
	return &OverviewHandlers{videoRepo: videoRepo}
}

// categoryOverview is a single trending category in the overview response.
type categoryOverview struct {
	Category string          `json:"category"`
	Videos   []*domain.Video `json:"videos"`
}

// overviewResponse is the top-level response for GET /api/v1/overviews/videos.
type overviewResponse struct {
	Categories []categoryOverview `json:"categories"`
	Channels   []categoryOverview `json:"channels"`
	Tags       []categoryOverview `json:"tags"`
}

// GetOverview handles GET /api/v1/overviews/videos.
// Returns trending videos grouped by categories, channels, and tags.
func (h *OverviewHandlers) GetOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Fetch a reasonable set of recent popular videos to build the overview.
	videos, _, err := h.videoRepo.List(ctx, &domain.VideoSearchRequest{
		Sort:  "views",
		Order: "desc",
		Limit: 24,
	})
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to load video overview"))
		return
	}

	// Group videos by category.
	catMap := make(map[string][]*domain.Video)
	tagMap := make(map[string][]*domain.Video)
	chanMap := make(map[string][]*domain.Video)

	for _, v := range videos {
		cat := "Uncategorized"
		if v.Category != nil && v.Category.Name != "" {
			cat = v.Category.Name
		}
		if len(catMap[cat]) < 4 {
			catMap[cat] = append(catMap[cat], v)
		}

		ch := v.ChannelID.String()
		if len(chanMap[ch]) < 4 {
			chanMap[ch] = append(chanMap[ch], v)
		}

		for _, tag := range v.Tags {
			if len(tagMap[tag]) < 4 {
				tagMap[tag] = append(tagMap[tag], v)
			}
		}
	}

	resp := overviewResponse{
		Categories: mapToOverview(catMap),
		Channels:   mapToOverview(chanMap),
		Tags:       mapToOverview(tagMap),
	}

	shared.WriteJSON(w, http.StatusOK, resp)
}

func mapToOverview(m map[string][]*domain.Video) []categoryOverview {
	result := make([]categoryOverview, 0, len(m))
	for k, v := range m {
		result = append(result, categoryOverview{Category: k, Videos: v})
	}
	if len(result) == 0 {
		return []categoryOverview{}
	}
	return result
}
