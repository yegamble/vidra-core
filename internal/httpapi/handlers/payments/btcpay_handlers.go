package payments

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	ucpayments "vidra-core/internal/usecase/payments"

	"github.com/go-chi/chi/v5"
)

// BTCPayHandler handles HTTP requests for BTCPay payment operations.
type BTCPayHandler struct {
	service *ucpayments.BTCPayService
}

// NewBTCPayHandler creates a new BTCPay handler.
func NewBTCPayHandler(service *ucpayments.BTCPayService) *BTCPayHandler {
	return &BTCPayHandler{service: service}
}

// CreateInvoice handles POST /api/v1/payments/invoices
func (h *BTCPayHandler) CreateInvoice(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Unauthorized"))
		return
	}

	var req struct {
		AmountSats    int64                  `json:"amount_sats"`
		Currency      string                 `json:"currency,omitempty"`
		PaymentMethod string                 `json:"payment_method,omitempty"`
		Metadata      map[string]interface{} `json:"metadata,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "Invalid request body"))
		return
	}

	if req.AmountSats <= 0 {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_AMOUNT", "Amount must be greater than zero"))
		return
	}

	switch req.PaymentMethod {
	case "", "on_chain", "lightning", "both":
		// valid
	default:
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_PAYMENT_METHOD", "payment_method must be one of: on_chain, lightning, both"))
		return
	}

	invoice, err := h.service.CreateInvoice(r.Context(), userID, req.AmountSats, req.Currency, req.PaymentMethod, req.Metadata)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("CREATE_INVOICE_FAILED", "Failed to create invoice"))
		return
	}

	shared.WriteJSON(w, http.StatusCreated, invoice)
}

// GetInvoice handles GET /api/v1/payments/invoices/{id}
func (h *BTCPayHandler) GetInvoice(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Unauthorized"))
		return
	}

	invoiceID := chi.URLParam(r, "id")
	if invoiceID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "Missing invoice ID"))
		return
	}

	invoice, err := h.service.GetInvoice(r.Context(), invoiceID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, invoice)
}

// ListInvoices handles GET /api/v1/payments/invoices
func (h *BTCPayHandler) ListInvoices(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Unauthorized"))
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("count"))
	if limit <= 0 {
		limit = 15
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("start"))
	if offset < 0 {
		offset = 0
	}

	invoices, err := h.service.GetPaymentsByUser(r.Context(), userID, limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("LIST_INVOICES_FAILED", "Failed to list invoices"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, invoices)
}

// WebhookCallback handles POST /api/v1/payments/webhooks/btcpay
// This endpoint does NOT require JWT auth — it uses HMAC signature verification.
func (h *BTCPayHandler) WebhookCallback(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 65536))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "Failed to read request body"))
		return
	}

	signature := r.Header.Get("BTCPay-Sig")
	if !h.service.ValidateWebhookSignature(body, signature) {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("INVALID_SIGNATURE", "Invalid webhook signature"))
		return
	}

	var event domain.BTCPayWebhookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "Invalid webhook payload"))
		return
	}
	event.OriginalPayload = body

	if err := h.service.ProcessWebhook(r.Context(), &event); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("WEBHOOK_FAILED", "Failed to process webhook"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
