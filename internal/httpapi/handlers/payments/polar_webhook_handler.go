package payments

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	ucpayments "vidra-core/internal/usecase/payments"
)

// PolarWebhookHandler handles POST /api/v1/payments/webhooks/polar.
//
// Polar's payload shape is mapped to ucpayments.PolarWebhookEvent here so the
// service layer remains transport-agnostic and tests can exercise the handler
// without crafting full Polar JSON.
type PolarWebhookHandler struct {
	service *ucpayments.PolarWebhookService
}

// NewPolarWebhookHandler builds the handler.
func NewPolarWebhookHandler(service *ucpayments.PolarWebhookService) *PolarWebhookHandler {
	return &PolarWebhookHandler{service: service}
}

// polarRawEvent mirrors the subset of Polar's webhook envelope we consume.
type polarRawEvent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data struct {
		ID                 string            `json:"id"` // subscription/checkout id
		SubscriptionID     string            `json:"subscription_id,omitempty"`
		ExternalCustomerID string            `json:"external_customer_id,omitempty"`
		Metadata           map[string]string `json:"metadata,omitempty"`
		CurrentPeriodEnd   *time.Time        `json:"current_period_end,omitempty"`
	} `json:"data"`
}

// Receive verifies the HMAC and dispatches to the service.
func (h *PolarWebhookHandler) Receive(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "failed to read body"))
		return
	}

	// Phase 9 — prefer Standard Webhooks (Polar's actual signing scheme); fall
	// back to bare-HMAC for sandbox tooling that doesn't yet send the SW
	// headers. Either way, an unsigned/invalid request becomes 401.
	whID := r.Header.Get("webhook-id")
	whTimestamp := r.Header.Get("webhook-timestamp")
	whSignature := r.Header.Get("webhook-signature")
	if whID != "" && whTimestamp != "" && whSignature != "" {
		if err := h.service.VerifyStandardWebhook(whID, whTimestamp, whSignature, body); err != nil {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("INVALID_SIGNATURE", "polar webhook signature verification failed"))
			return
		}
	} else {
		signature := r.Header.Get("Polar-Webhook-Signature")
		if signature == "" {
			signature = r.Header.Get("X-Polar-Signature")
		}
		if err := h.service.VerifySignature(body, signature); err != nil {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("INVALID_SIGNATURE", "polar webhook signature verification failed"))
			return
		}
	}

	var raw polarRawEvent
	if err := json.Unmarshal(body, &raw); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "invalid polar payload"))
		return
	}

	subscriptionID := raw.Data.SubscriptionID
	if subscriptionID == "" {
		// Polar's `subscription.*` events put the id directly in data.id.
		subscriptionID = raw.Data.ID
	}

	evt := ucpayments.PolarWebhookEvent{
		EventID:            raw.ID,
		EventType:          raw.Type,
		SubscriptionID:     subscriptionID,
		ExternalCustomerID: raw.Data.ExternalCustomerID,
		Metadata:           raw.Data.Metadata,
		CurrentPeriodEnd:   raw.Data.CurrentPeriodEnd,
	}

	if err := h.service.Handle(r.Context(), evt); err != nil {
		switch {
		case errors.Is(err, ucpayments.ErrPolarMissingUser):
			shared.WriteError(w, http.StatusUnprocessableEntity, domain.NewDomainError("POLAR_USER_UNRESOLVABLE", err.Error()))
		case errors.Is(err, ucpayments.ErrPolarMissingChannel), errors.Is(err, ucpayments.ErrPolarBadTier):
			shared.WriteError(w, http.StatusUnprocessableEntity, domain.NewDomainError("POLAR_METADATA_INVALID", err.Error()))
		default:
			slog.Error("polar webhook handle failed", "err", err)
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("POLAR_WEBHOOK_FAILED", "polar webhook processing failed"))
		}
		return
	}

	w.WriteHeader(http.StatusOK)
}
