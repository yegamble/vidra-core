package video

import (
	"net/http"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/port"
)

// SearchChannelsHandler handles GET /api/v1/search/video-channels.
func SearchChannelsHandler(repo port.ChannelRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("search")
		if query == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_QUERY", "Search query is required"))
			return
		}

		page, limit, offset, pageSize := shared.ParsePagination(r, 20)

		params := domain.ChannelListParams{
			Search:   query,
			Page:     page,
			PageSize: pageSize,
		}

		result, err := repo.List(r.Context(), params)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("SEARCH_FAILED", "Failed to search channels"))
			return
		}

		meta := &shared.Meta{
			Total:    int64(result.Total),
			Limit:    limit,
			Offset:   offset,
			Page:     page,
			PageSize: pageSize,
		}

		shared.WriteJSONWithMeta(w, http.StatusOK, result.Data, meta)
	}
}

// SearchPlaylistsHandler handles GET /api/v1/search/video-playlists.
func SearchPlaylistsHandler(repo port.PlaylistRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("search")
		if query == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_QUERY", "Search query is required"))
			return
		}

		page, limit, offset, pageSize := shared.ParsePagination(r, 20)

		opts := domain.PlaylistListOptions{
			Search: query,
			Limit:  limit,
			Offset: offset,
		}

		playlists, total, err := repo.List(r.Context(), opts)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("SEARCH_FAILED", "Failed to search playlists"))
			return
		}

		meta := &shared.Meta{
			Total:    int64(total),
			Limit:    limit,
			Offset:   offset,
			Page:     page,
			PageSize: pageSize,
		}

		shared.WriteJSONWithMeta(w, http.StatusOK, playlists, meta)
	}
}
