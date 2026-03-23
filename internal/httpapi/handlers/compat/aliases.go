package compat

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/port"
	"vidra-core/internal/usecase"
	ucchannel "vidra-core/internal/usecase/channel"
)

// AliasHandlers provides thin route aliases that resolve PeerTube-style URL
// patterns to Vidra Core's canonical endpoints.
type AliasHandlers struct {
	captionRepo    port.CaptionRepository
	channelService *ucchannel.Service
	videoRepo      usecase.VideoRepository
}

// NewAliasHandlers creates handlers for PeerTube-compatible URL aliases.
func NewAliasHandlers(
	captionRepo port.CaptionRepository,
	channelService *ucchannel.Service,
	videoRepo usecase.VideoRepository,
) *AliasHandlers {
	return &AliasHandlers{
		captionRepo:    captionRepo,
		channelService: channelService,
		videoRepo:      videoRepo,
	}
}

// ResolveChannelHandle resolves a channel handle to a channel ID and injects
// it as the "id" URL parameter for downstream handlers.
//
// PUT/DELETE /api/v1/video-channels/{handle}
// --> resolves handle to channel ID, then delegates.
func (h *AliasHandlers) ResolveChannelHandle(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handle := chi.URLParam(r, "handle")
		if handle == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_HANDLE", "Channel handle is required"))
			return
		}

		ch, err := h.channelService.GetChannelByHandle(r.Context(), handle)
		if err != nil {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("CHANNEL_NOT_FOUND", "Channel not found for handle: "+handle))
			return
		}

		// Inject the resolved channel ID into the route context.
		rctx := chi.RouteContext(r.Context())
		rctx.URLParams.Add("id", ch.ID.String())

		next(w, r)
	}
}
