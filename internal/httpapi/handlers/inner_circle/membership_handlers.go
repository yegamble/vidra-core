package inner_circle

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	icusecase "vidra-core/internal/usecase/inner_circle"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// MembershipHandler exposes subscribe/cancel/list endpoints.
type MembershipHandler struct {
	service *icusecase.MembershipService
}

// NewMembershipHandler builds the handler.
func NewMembershipHandler(service *icusecase.MembershipService) *MembershipHandler {
	return &MembershipHandler{service: service}
}

func writeUnauthorized(w http.ResponseWriter) {
	shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "authentication required"))
}

func userIDFromContext(r *http.Request) (uuid.UUID, bool) {
	str, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || str == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(str)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// SubscribeBTCPay handles POST /api/v1/channels/{id}/inner-circle/subscribe.
// Phase 9 only supports the BTCPay rail here — Polar checkout creation stays
// in vidra-user (single Polar caller).
func (h *MembershipHandler) SubscribeBTCPay(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r)
	if !ok {
		writeUnauthorized(w)
		return
	}
	channelID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "channel id must be a UUID"))
		return
	}
	var body struct {
		TierID string `json:"tierId"`
		Method string `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "invalid request body"))
		return
	}
	if body.Method != "" && body.Method != "btcpay" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("UNSUPPORTED_METHOD", "this endpoint only handles method=btcpay; use the Polar checkout in the frontend for method=polar"))
		return
	}

	res, err := h.service.SubscribeBTCPay(r.Context(), userID, channelID, body.TierID)
	if err != nil {
		switch {
		case errors.Is(err, icusecase.ErrChannelNotFound):
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("CHANNEL_NOT_FOUND", "channel not found"))
		case errors.Is(err, icusecase.ErrTierNotFound):
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("TIER_NOT_FOUND", "tier not found or disabled"))
		default:
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("SUBSCRIBE_FAILED", err.Error()))
		}
		return
	}
	shared.WriteJSON(w, http.StatusCreated, res)
}

// CreatePendingPolar handles POST /api/v1/channels/{id}/inner-circle/pending-polar.
// Frontend calls this after opening Polar checkout so the pending row is visible
// in getMyMemberships(includePending=true) before the webhook arrives.
func (h *MembershipHandler) CreatePendingPolar(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r)
	if !ok {
		writeUnauthorized(w)
		return
	}
	channelID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "channel id must be a UUID"))
		return
	}
	var body struct {
		TierID         string `json:"tierId"`
		PolarSessionID string `json:"polarSessionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "invalid request body"))
		return
	}
	id, err := h.service.CreatePendingPolar(r.Context(), userID, channelID, body.TierID, body.PolarSessionID)
	if err != nil {
		if errors.Is(err, icusecase.ErrChannelNotFound) {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("CHANNEL_NOT_FOUND", "channel not found"))
			return
		}
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("PENDING_FAILED", err.Error()))
		return
	}
	shared.WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"membership_id": id.String(),
		"status":        "pending",
	})
}

// Cancel handles DELETE /api/v1/inner-circle/memberships/{id}.
func (h *MembershipHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r)
	if !ok {
		writeUnauthorized(w)
		return
	}
	membershipID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "membership id must be a UUID"))
		return
	}
	if err := h.service.CancelMine(r.Context(), userID, membershipID); err != nil {
		if errors.Is(err, icusecase.ErrMembershipNotFound) {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("MEMBERSHIP_NOT_FOUND", "membership not found or not yours"))
			return
		}
		// Any other failure: don't masquerade as 404.
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("CANCEL_FAILED", "failed to cancel membership"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListMine handles GET /api/v1/inner-circle/memberships/me.
// Query: include_pending=true|false (default false).
func (h *MembershipHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r)
	if !ok {
		writeUnauthorized(w)
		return
	}
	includePending, _ := strconv.ParseBool(r.URL.Query().Get("include_pending"))
	rows, err := h.service.ListMine(r.Context(), userID, includePending)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("LIST_FAILED", "failed to list memberships"))
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": toMembershipResponses(rows)})
}

// ListChannelMembers handles GET /api/v1/channels/{id}/inner-circle/members.
// Creator-only.
func (h *MembershipHandler) ListChannelMembers(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r)
	if !ok {
		writeUnauthorized(w)
		return
	}
	channelID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "channel id must be a UUID"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	rows, err := h.service.ListByChannel(r.Context(), channelID, userID, limit, offset)
	if err != nil {
		switch {
		case errors.Is(err, icusecase.ErrChannelNotFound):
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("CHANNEL_NOT_FOUND", "channel not found"))
		case errors.Is(err, icusecase.ErrNotChannelOwner):
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("NOT_CHANNEL_OWNER", "only the channel owner can list members"))
		default:
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("LIST_FAILED", "failed to list members"))
		}
		return
	}
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": toMembershipResponses(rows)})
}

type membershipResponse struct {
	ID                  string  `json:"id"`
	UserID              string  `json:"userId"`
	ChannelID           string  `json:"channelId"`
	TierID              string  `json:"tierId"`
	Status              string  `json:"status"`
	StartedAt           *string `json:"startedAt,omitempty"`
	ExpiresAt           string  `json:"expiresAt"`
	PolarSubscriptionID *string `json:"polarSubscriptionId,omitempty"`
	BTCPayInvoiceID     *string `json:"btcpayInvoiceId,omitempty"`
}

func toMembershipResponses(rows []domain.InnerCircleMembership) []membershipResponse {
	out := make([]membershipResponse, len(rows))
	for i, m := range rows {
		var startedAt *string
		if m.StartedAt != nil {
			s := m.StartedAt.UTC().Format("2006-01-02T15:04:05Z")
			startedAt = &s
		}
		var btcpayID *string
		if m.BTCPayInvoiceID != nil {
			s := m.BTCPayInvoiceID.String()
			btcpayID = &s
		}
		out[i] = membershipResponse{
			ID:                  m.ID.String(),
			UserID:              m.UserID.String(),
			ChannelID:           m.ChannelID.String(),
			TierID:              m.TierID,
			Status:              string(m.Status),
			StartedAt:           startedAt,
			ExpiresAt:           m.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"),
			PolarSubscriptionID: m.PolarSubscriptionID,
			BTCPayInvoiceID:     btcpayID,
		}
	}
	return out
}
