package domain

import (
	"testing"
)

func TestInvoiceStatusValues(t *testing.T) {
	tests := []struct {
		name   string
		status InvoiceStatus
		want   string
	}{
		{"new", InvoiceStatusNew, "New"},
		{"processing", InvoiceStatusProcessing, "Processing"},
		{"settled", InvoiceStatusSettled, "Settled"},
		{"invalid", InvoiceStatusInvalid, "Invalid"},
		{"expired", InvoiceStatusExpired, "Expired"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.status) != tt.want {
				t.Errorf("InvoiceStatus = %q, want %q", tt.status, tt.want)
			}
		})
	}
}

func TestBTCPayDomainErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode string
	}{
		{"invoice not found", ErrInvoiceNotFound, "INVOICE_NOT_FOUND"},
		{"invoice expired", ErrInvoiceExpired, "INVOICE_EXPIRED"},
		{"invalid amount", ErrInvalidAmount, "INVALID_AMOUNT"},
		{"btcpay unavailable", ErrBTCPayUnavailable, "BTCPAY_UNAVAILABLE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			domainErr, ok := tt.err.(DomainError)
			if !ok {
				t.Fatalf("expected DomainError, got %T", tt.err)
			}
			if domainErr.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", domainErr.Code, tt.wantCode)
			}
		})
	}
}

func TestBTCPayInvoiceFields(t *testing.T) {
	invoice := BTCPayInvoice{
		ID:              "test-id",
		BTCPayInvoiceID: "btcpay-123",
		UserID:          "user-456",
		AmountSats:      100000,
		Currency:        "BTC",
		Status:          InvoiceStatusNew,
	}

	if invoice.AmountSats != 100000 {
		t.Errorf("AmountSats = %d, want 100000", invoice.AmountSats)
	}
	if invoice.Status != InvoiceStatusNew {
		t.Errorf("Status = %q, want %q", invoice.Status, InvoiceStatusNew)
	}
}
