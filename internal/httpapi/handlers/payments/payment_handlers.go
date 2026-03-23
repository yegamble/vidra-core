package payments

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/port"
	"athena/internal/security"

	"github.com/go-chi/chi/v5"
)

// PaymentHandler handles HTTP requests for payment operations
type PaymentHandler struct {
	service port.PaymentService
}

// NewPaymentHandler creates a new payment handler
func NewPaymentHandler(service port.PaymentService) *PaymentHandler {
	return &PaymentHandler{
		service: service,
	}
}

// CreateWallet handles POST /api/v1/payments/wallet
func (h *PaymentHandler) CreateWallet(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Unauthorized"))
		return
	}

	wallet, err := h.service.CreateWallet(r.Context(), userID)
	if err != nil {
		if errors.Is(err, domain.ErrWalletAlreadyExists) {
			shared.WriteError(w, http.StatusConflict, domain.NewDomainError("WALLET_EXISTS", "Wallet already exists for this user"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("CREATE_WALLET_FAILED", "Failed to create wallet"))
		return
	}

	shared.WriteJSON(w, http.StatusCreated, wallet)
}

// GetWallet handles GET /api/v1/payments/wallet
func (h *PaymentHandler) GetWallet(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Unauthorized"))
		return
	}

	wallet, err := h.service.GetWallet(r.Context(), userID)
	if err != nil {
		if errors.Is(err, domain.ErrWalletNotFound) {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("WALLET_NOT_FOUND", "Wallet not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("GET_WALLET_FAILED", "Failed to retrieve wallet"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, wallet)
}

// CreatePaymentIntent handles POST /api/v1/payments/intent
func (h *PaymentHandler) CreatePaymentIntent(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Unauthorized"))
		return
	}

	var req struct {
		AmountIOTA int64   `json:"amount_iota"`
		VideoID    *string `json:"video_id,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "Invalid request body"))
		return
	}

	if req.AmountIOTA <= 0 {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_AMOUNT", "Invalid amount: must be greater than zero"))
		return
	}

	if err := security.ValidateOptionalUUID(req.VideoID); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_VIDEO_ID", "Invalid video_id: must be a valid UUID"))
		return
	}

	intent, err := h.service.CreatePaymentIntent(r.Context(), userID, req.VideoID, req.AmountIOTA)
	if err != nil {
		if errors.Is(err, domain.ErrVideoNotFound) {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("CREATE_INTENT_FAILED", "Failed to create payment intent"))
		return
	}

	shared.WriteJSON(w, http.StatusCreated, intent)
}

// GetPaymentIntent handles GET /api/v1/payments/intent/:id
func (h *PaymentHandler) GetPaymentIntent(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Unauthorized"))
		return
	}

	intentID := chi.URLParam(r, "id")
	if intentID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "Missing intent ID"))
		return
	}

	intent, err := h.service.GetPaymentIntent(r.Context(), intentID)
	if err != nil {
		if errors.Is(err, domain.ErrPaymentIntentNotFound) {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("INTENT_NOT_FOUND", "Payment intent not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("GET_INTENT_FAILED", "Failed to retrieve payment intent"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, intent)
}

// GetTransactionHistory handles GET /api/v1/payments/transactions
func (h *PaymentHandler) GetTransactionHistory(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Unauthorized"))
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	transactions, err := h.service.GetTransactionHistory(r.Context(), userID, limit, offset)
	if err != nil {
		if errors.Is(err, domain.ErrWalletNotFound) {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("WALLET_NOT_FOUND", "Wallet not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("GET_TRANSACTIONS_FAILED", "Failed to retrieve transaction history"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, transactions)
}
