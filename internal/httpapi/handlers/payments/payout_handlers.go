package payments

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	ucpayments "vidra-core/internal/usecase/payments"

	"github.com/go-chi/chi/v5"
)

// PayoutHandler exposes creator-side payout endpoints.
type PayoutHandler struct {
	svc *ucpayments.PayoutService
}

func NewPayoutHandler(svc *ucpayments.PayoutService) *PayoutHandler {
	return &PayoutHandler{svc: svc}
}

// Request handles POST /api/v1/payments/payouts.
func (h *PayoutHandler) Request(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Unauthorized"))
		return
	}
	var body domain.PayoutRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "Invalid request body"))
		return
	}
	p, err := h.svc.RequestPayout(r.Context(), userID, body)
	if err != nil {
		writePayoutError(w, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, p)
}

// ListMine handles GET /api/v1/payments/payouts/me.
func (h *PayoutHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Unauthorized"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("count"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("start"))
	rows, total, err := h.svc.ListMine(r.Context(), userID, limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("LIST_FAILED", "Failed to list payouts"))
		return
	}
	shared.WriteJSONWithMeta(w, http.StatusOK, rows, &shared.Meta{Total: int64(total), Limit: limit, Offset: offset})
}

// Cancel handles DELETE /api/v1/payments/payouts/{id}.
func (h *PayoutHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Unauthorized"))
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.svc.CancelPayout(r.Context(), id, userID); err != nil {
		writePayoutError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AdminPayoutHandler exposes admin-only payout management endpoints.
type AdminPayoutHandler struct {
	svc *ucpayments.PayoutService
}

func NewAdminPayoutHandler(svc *ucpayments.PayoutService) *AdminPayoutHandler {
	return &AdminPayoutHandler{svc: svc}
}

// ListPending handles GET /api/v1/payments/admin/payouts.
func (h *AdminPayoutHandler) ListPending(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("count"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("start"))
	rows, total, err := h.svc.ListPending(r.Context(), limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("LIST_FAILED", "Failed to list pending payouts"))
		return
	}
	shared.WriteJSONWithMeta(w, http.StatusOK, rows, &shared.Meta{Total: int64(total), Limit: limit, Offset: offset})
}

// Approve handles PATCH /api/v1/payments/admin/payouts/{id}/approve.
func (h *AdminPayoutHandler) Approve(w http.ResponseWriter, r *http.Request) {
	adminID, _ := r.Context().Value(middleware.UserIDKey).(string)
	id := chi.URLParam(r, "id")
	if err := h.svc.ApprovePayout(r.Context(), id, adminID); err != nil {
		writePayoutError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Reject handles PATCH /api/v1/payments/admin/payouts/{id}/reject.
func (h *AdminPayoutHandler) Reject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := h.svc.RejectPayout(r.Context(), id, body.Reason); err != nil {
		writePayoutError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// MarkExecuted handles PATCH /api/v1/payments/admin/payouts/{id}/mark-executed.
func (h *AdminPayoutHandler) MarkExecuted(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Txid string `json:"txid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "Invalid body"))
		return
	}
	if err := h.svc.MarkExecuted(r.Context(), id, body.Txid); err != nil {
		writePayoutError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writePayoutError maps domain errors to HTTP status codes.
func writePayoutError(w http.ResponseWriter, err error) {
	var de domain.DomainError
	if errors.As(err, &de) {
		switch de {
		case domain.ErrPayoutNotFound:
			shared.WriteError(w, http.StatusNotFound, err)
			return
		case domain.ErrPayoutInvalidStatus:
			shared.WriteError(w, http.StatusConflict, err)
			return
		case domain.ErrInsufficientBalance:
			shared.WriteError(w, http.StatusConflict, err)
			return
		case domain.ErrPayoutForbidden:
			shared.WriteError(w, http.StatusForbidden, err)
			return
		case domain.ErrPayoutInvalidDest, domain.ErrPayoutAmountTooSmall:
			shared.WriteError(w, http.StatusBadRequest, err)
			return
		}
	}
	shared.WriteError(w, http.StatusInternalServerError, err)
}
