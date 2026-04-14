package compat

import (
	"net/http"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/port"
)

// OverviewHandlers provides PeerTube-compatible overview endpoints.
type OverviewHandlers struct {
	videoRepo port.VideoRepository
}

// NewOverviewHandlers creates handlers for the /overviews/* endpoints.
func NewOverviewHandlers(videoRepo port.VideoRepository) *OverviewHandlers {
	return &OverviewHandlers{videoRepo: videoRepo}
}

// GetVideoOverview handles GET /api/v1/overviews/videos
// PeerTube returns a composite response with categories, channels, and tags,
// each containing a small selection of videos.
func (h *OverviewHandlers) GetVideoOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	req := &domain.VideoSearchRequest{
		Limit:  12,
		Offset: 0,
		Sort:   "-trending",
	}

	videos, _, err := h.videoRepo.List(ctx, req)
	if err != nil {
		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"categories": []interface{}{},
			"channels":   []interface{}{},
			"tags":       []interface{}{},
		})
		return
	}

	categoryMap := make(map[string][]interface{})
	for _, v := range videos {
		cat := "Uncategorized"
		if v.Category != nil && v.Category.Name != "" {
			cat = v.Category.Name
		}
		if len(categoryMap[cat]) < 4 {
			categoryMap[cat] = append(categoryMap[cat], v)
		}
	}

	categories := make([]map[string]interface{}, 0)
	for name, vids := range categoryMap {
		if len(categories) >= 3 {
			break
		}
		categories = append(categories, map[string]interface{}{
			"category": map[string]interface{}{
				"id":    name,
				"label": name,
			},
			"videos": vids,
		})
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"categories": categories,
		"channels":   []interface{}{},
		"tags":       []interface{}{},
	})
}
