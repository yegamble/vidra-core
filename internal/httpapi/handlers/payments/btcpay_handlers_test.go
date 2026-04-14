package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vidra-core/internal/middleware"
	ucpayments "vidra-core/internal/usecase/payments"

	"github.com/stretchr/testify/assert"
)

func TestBTCPayHandler_CreateInvoice_Unauthorized(t *testing.T) {
	handler := NewBTCPayHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/invoices",
		strings.NewReader(`{"amount_sats": 100000}`))
	w := httptest.NewRecorder()

	handler.CreateInvoice(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestBTCPayHandler_CreateInvoice_InvalidBody(t *testing.T) {
	handler := NewBTCPayHandler(nil)

	ctx := context.WithValue(context.Background(), middleware.UserIDKey, "user-1")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/invoices",
		strings.NewReader("not json")).WithContext(ctx)
	w := httptest.NewRecorder()

	handler.CreateInvoice(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBTCPayHandler_CreateInvoice_InvalidAmount(t *testing.T) {
	handler := NewBTCPayHandler(nil)

	ctx := context.WithValue(context.Background(), middleware.UserIDKey, "user-1")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/invoices",
		strings.NewReader(`{"amount_sats": 0}`)).WithContext(ctx)
	w := httptest.NewRecorder()

	handler.CreateInvoice(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(w.Body).Decode(&envelope)
	assert.Equal(t, "INVALID_AMOUNT", envelope.Error.Code)
}

func TestBTCPayHandler_GetInvoice_Unauthorized(t *testing.T) {
	handler := NewBTCPayHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/invoices/inv-1", nil)
	w := httptest.NewRecorder()

	handler.GetInvoice(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestBTCPayHandler_ListInvoices_Unauthorized(t *testing.T) {
	handler := NewBTCPayHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/invoices", nil)
	w := httptest.NewRecorder()

	handler.ListInvoices(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestBTCPayHandler_WebhookCallback_InvalidBody(t *testing.T) {
	svc := ucpayments.NewBTCPayService(nil, nil, "")
	handler := NewBTCPayHandler(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/webhooks/btcpay",
		strings.NewReader(""))
	w := httptest.NewRecorder()

	handler.WebhookCallback(w, req)

	// Empty body will fail JSON unmarshaling
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
