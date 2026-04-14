package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"vidra-core/internal/domain"

	"github.com/stretchr/testify/assert"
)

func TestBTCPayService_CreateInvoice_InvalidAmount(t *testing.T) {
	svc := NewBTCPayService(nil, nil, "")

	_, err := svc.CreateInvoice(context.Background(), "user-1", 0, "BTC", nil)
	assert.ErrorIs(t, err, domain.ErrInvalidAmount)

	_, err = svc.CreateInvoice(context.Background(), "user-1", -100, "BTC", nil)
	assert.ErrorIs(t, err, domain.ErrInvalidAmount)
}

func TestBTCPayService_ValidateWebhookSignature(t *testing.T) {
	svc := NewBTCPayService(nil, nil, "test-secret")

	t.Run("valid signature", func(t *testing.T) {
		payload := []byte(`{"invoiceId":"inv-1","type":"InvoiceSettled"}`)
		mac := hmac.New(sha256.New, []byte("test-secret"))
		mac.Write(payload)
		sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		assert.True(t, svc.ValidateWebhookSignature(payload, sig))
	})

	t.Run("invalid signature", func(t *testing.T) {
		payload := []byte(`{"invoiceId":"inv-1"}`)
		assert.False(t, svc.ValidateWebhookSignature(payload, "sha256=invalid"))
	})

	t.Run("empty secret allows all", func(t *testing.T) {
		svcNoSecret := NewBTCPayService(nil, nil, "")
		payload := []byte(`{"any":"data"}`)
		assert.True(t, svcNoSecret.ValidateWebhookSignature(payload, "anything"))
	})
}

func TestBTCPayService_ProcessWebhook_UnknownType(t *testing.T) {
	svc := NewBTCPayService(nil, nil, "")
	event := &domain.BTCPayWebhookEvent{
		Type:      "UnknownEvent",
		InvoiceID: "inv-1",
	}

	err := svc.ProcessWebhook(context.Background(), event)
	assert.NoError(t, err)
}

func TestBTCPayService_ProcessWebhook_MissingInvoiceID(t *testing.T) {
	svc := NewBTCPayService(nil, nil, "")
	event := &domain.BTCPayWebhookEvent{
		Type:      "InvoiceSettled",
		InvoiceID: "",
	}

	err := svc.ProcessWebhook(context.Background(), event)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing invoice ID")
}
