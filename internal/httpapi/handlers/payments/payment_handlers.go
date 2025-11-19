package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"athena/internal/domain"
	"athena/internal/security"

	"github.com/go-chi/chi/v5"
)

// PaymentService defines the interface for payment operations
type PaymentService interface {
	CreateWallet(ctx context.Context, userID string) (*domain.IOTAWallet, error)
	GetWallet(ctx context.Context, userID string) (*domain.IOTAWallet, error)
	GetWalletBalance(ctx context.Context, userID string) (int64, error)
	CreatePaymentIntent(ctx context.Context, userID string, videoID *string, amount int64) (*domain.IOTAPaymentIntent, error)
	GetPaymentIntent(ctx context.Context, intentID string) (*domain.IOTAPaymentIntent, error)
	GetTransactionHistory(ctx context.Context, userID string, limit, offset int) ([]*domain.IOTATransaction, error)
}

// PaymentHandler handles HTTP requests for payment operations
type PaymentHandler struct {
	service PaymentService
}

// NewPaymentHandler creates a new payment handler
func NewPaymentHandler(service PaymentService) *PaymentHandler {
	return &PaymentHandler{
		service: service,
	}
}

// CreateWallet handles POST /api/v1/payments/wallet
func (h *PaymentHandler) CreateWallet(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("user_id").(string)
	if !ok || userID == "" {
		h.errorResponse(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	wallet, err := h.service.CreateWallet(r.Context(), userID)
	if err != nil {
		if err == domain.ErrWalletAlreadyExists {
			h.errorResponse(w, "Wallet already exists for this user", http.StatusConflict)
			return
		}
		h.errorResponse(w, "Failed to create wallet", http.StatusInternalServerError)
		return
	}

	h.successResponse(w, wallet, http.StatusCreated)
}

// GetWallet handles GET /api/v1/payments/wallet
func (h *PaymentHandler) GetWallet(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("user_id").(string)
	if !ok || userID == "" {
		h.errorResponse(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	wallet, err := h.service.GetWallet(r.Context(), userID)
	if err != nil {
		if err == domain.ErrWalletNotFound {
			h.errorResponse(w, "Wallet not found", http.StatusNotFound)
			return
		}
		h.errorResponse(w, "Failed to retrieve wallet", http.StatusInternalServerError)
		return
	}

	h.successResponse(w, wallet, http.StatusOK)
}

// CreatePaymentIntent handles POST /api/v1/payments/intent
func (h *PaymentHandler) CreatePaymentIntent(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("user_id").(string)
	if !ok || userID == "" {
		h.errorResponse(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		AmountIOTA int64   `json:"amount_iota"`
		VideoID    *string `json:"video_id,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.AmountIOTA <= 0 {
		h.errorResponse(w, "Invalid amount: must be greater than zero", http.StatusBadRequest)
		return
	}

	// SECURITY FIX: Validate video_id is a valid UUID to prevent SQL injection
	// This validation prevents malicious payloads like "'; DROP TABLE videos; --"
	if err := security.ValidateOptionalUUID(req.VideoID); err != nil {
		h.errorResponse(w, "Invalid video_id: must be a valid UUID", http.StatusBadRequest)
		return
	}

	intent, err := h.service.CreatePaymentIntent(r.Context(), userID, req.VideoID, req.AmountIOTA)
	if err != nil {
		h.errorResponse(w, "Failed to create payment intent", http.StatusInternalServerError)
		return
	}

	h.successResponse(w, intent, http.StatusCreated)
}

// GetPaymentIntent handles GET /api/v1/payments/intent/:id
func (h *PaymentHandler) GetPaymentIntent(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("user_id").(string)
	if !ok || userID == "" {
		h.errorResponse(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	intentID := chi.URLParam(r, "id")
	if intentID == "" {
		h.errorResponse(w, "Missing intent ID", http.StatusBadRequest)
		return
	}

	intent, err := h.service.GetPaymentIntent(r.Context(), intentID)
	if err != nil {
		if err == domain.ErrPaymentIntentNotFound {
			h.errorResponse(w, "Payment intent not found", http.StatusNotFound)
			return
		}
		h.errorResponse(w, "Failed to retrieve payment intent", http.StatusInternalServerError)
		return
	}

	h.successResponse(w, intent, http.StatusOK)
}

// GetTransactionHistory handles GET /api/v1/payments/transactions
func (h *PaymentHandler) GetTransactionHistory(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("user_id").(string)
	if !ok || userID == "" {
		h.errorResponse(w, "Unauthorized", http.StatusUnauthorized)
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
		if err == domain.ErrWalletNotFound {
			h.errorResponse(w, "Wallet not found", http.StatusNotFound)
			return
		}
		h.errorResponse(w, "Failed to retrieve transaction history", http.StatusInternalServerError)
		return
	}

	h.successResponse(w, transactions, http.StatusOK)
}

// Helper methods for responses
func (h *PaymentHandler) successResponse(w http.ResponseWriter, data interface{}, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data":    data,
	}); err != nil {
		// Best-effort fallback: write a minimal JSON error if encoding fails
		_, _ = w.Write([]byte(`{"success":false,"error":"failed to encode response"}`))
	}
}

func (h *PaymentHandler) errorResponse(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error":   message,
	}); err != nil {
		// Best-effort fallback: write a minimal JSON error if encoding fails
		_, _ = w.Write([]byte(`{"success":false,"error":"failed to encode error response"}`))
	}
}
