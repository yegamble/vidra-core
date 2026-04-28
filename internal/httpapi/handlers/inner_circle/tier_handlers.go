// Package inner_circle holds HTTP handlers for the Inner Circle API surface:
// tier CRUD, membership lifecycle, and channel posts.
package inner_circle

import (
	"encoding/json"
	"errors"
	"net/http"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	icusecase "vidra-core/internal/usecase/inner_circle"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// TierHandler exposes tier read + creator update routes.
type TierHandler struct {
	service *icusecase.TierService
}

// NewTierHandler wires the handler over a tier service.
func NewTierHandler(service *icusecase.TierService) *TierHandler {
	return &TierHandler{service: service}
}

// tierResponse is the JSON projection of a tier with member count. Field names
// are camelCase to match the frontend type at src/lib/api/types.ts.
type tierResponse struct {
	ID              string   `json:"id"`
	ChannelID       string   `json:"channelId"`
	TierID          string   `json:"tierId"`
	MonthlyUSDCents int      `json:"monthlyUsdCents"`
	MonthlySats     int64    `json:"monthlySats"`
	Perks           []string `json:"perks"`
	Enabled         bool     `json:"enabled"`
	MemberCount     int      `json:"memberCount"`
}

func toTierResponse(t domain.InnerCircleTierWithCount) tierResponse {
	perks := t.Perks
	if perks == nil {
		perks = []string{}
	}
	return tierResponse{
		ID:              t.ID.String(),
		ChannelID:       t.ChannelID.String(),
		TierID:          t.TierID,
		MonthlyUSDCents: t.MonthlyUSDCents,
		MonthlySats:     t.MonthlySats,
		Perks:           perks,
		Enabled:         t.Enabled,
		MemberCount:     t.MemberCount,
	}
}

// List handles GET /api/v1/channels/{id}/inner-circle/tiers.
// Public — returns every tier (enabled or not) with active member counts.
func (h *TierHandler) List(w http.ResponseWriter, r *http.Request) {
	channelID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "channel id must be a UUID"))
		return
	}
	tiers, err := h.service.List(r.Context(), channelID)
	if err != nil {
		if errors.Is(err, icusecase.ErrChannelNotFound) {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("CHANNEL_NOT_FOUND", "channel not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("LIST_TIERS_FAILED", "failed to list tiers"))
		return
	}
	resp := make([]tierResponse, len(tiers))
	for i, t := range tiers {
		resp[i] = toTierResponse(t)
	}
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": resp})
}

// Update handles PUT /api/v1/channels/{id}/inner-circle/tiers.
// Creator-only — accepts a 1-3 element array of tiers to upsert.
func (h *TierHandler) Update(w http.ResponseWriter, r *http.Request) {
	userIDStr, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userIDStr == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "authentication required"))
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "invalid auth context"))
		return
	}
	channelID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "channel id must be a UUID"))
		return
	}

	var body struct {
		Tiers []struct {
			TierID          string   `json:"tierId"`
			MonthlyUSDCents int      `json:"monthlyUsdCents"`
			MonthlySats     int64    `json:"monthlySats"`
			Perks           []string `json:"perks"`
			Enabled         bool     `json:"enabled"`
		} `json:"tiers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "invalid request body"))
		return
	}
	if len(body.Tiers) == 0 {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "tiers array required (1-3 entries)"))
		return
	}

	items := make([]icusecase.TierUpsertInput, len(body.Tiers))
	for i, t := range body.Tiers {
		items[i] = icusecase.TierUpsertInput{
			TierID:          t.TierID,
			MonthlyUSDCents: t.MonthlyUSDCents,
			MonthlySats:     t.MonthlySats,
			Perks:           t.Perks,
			Enabled:         t.Enabled,
		}
	}

	if err := h.service.Update(r.Context(), channelID, userID, items); err != nil {
		switch {
		case errors.Is(err, icusecase.ErrChannelNotFound):
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("CHANNEL_NOT_FOUND", "channel not found"))
		case errors.Is(err, icusecase.ErrNotChannelOwner):
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("NOT_CHANNEL_OWNER", "only the channel owner can update tiers"))
		case errors.Is(err, icusecase.ErrTooManyTiers):
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "tiers must be 1-3 entries with no duplicate tier_id"))
		case errors.Is(err, icusecase.ErrPerksTooLarge):
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "perks list too large"))
		default:
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("UPDATE_TIERS_FAILED", err.Error()))
		}
		return
	}

	tiers, err := h.service.List(r.Context(), channelID)
	if err != nil {
		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": []tierResponse{}})
		return
	}
	resp := make([]tierResponse, len(tiers))
	for i, t := range tiers {
		resp[i] = toTierResponse(t)
	}
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": resp})
}
