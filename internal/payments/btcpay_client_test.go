package payments

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"vidra-core/internal/domain"
)

func TestBTCPayClient_CreateInvoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "token test-api-key" {
			t.Errorf("expected auth header, got %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected json content type, got %q", r.Header.Get("Content-Type"))
		}

		var req CreateInvoiceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if req.Amount != 0.001 {
			t.Errorf("expected amount 0.001, got %f", req.Amount)
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(BTCPayInvoiceResponse{
			ID:           "inv-123",
			StoreID:      "store-456",
			Amount:       "0.001",
			Currency:     "BTC",
			Status:       "New",
			CheckoutLink: "https://btcpay.example.com/i/inv-123",
		})
	}))
	defer server.Close()

	client := NewBTCPayClient(server.URL, "test-api-key", "store-456")
	invoice, err := client.CreateInvoice(context.Background(), &CreateInvoiceRequest{
		Amount:   0.001,
		Currency: "BTC",
	})
	if err != nil {
		t.Fatalf("CreateInvoice failed: %v", err)
	}
	if invoice.ID != "inv-123" {
		t.Errorf("ID = %q, want inv-123", invoice.ID)
	}
	if invoice.Status != "New" {
		t.Errorf("Status = %q, want New", invoice.Status)
	}
	if invoice.CheckoutLink == "" {
		t.Error("CheckoutLink should not be empty")
	}
}

func TestBTCPayClient_GetInvoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(BTCPayInvoiceResponse{
			ID:       "inv-123",
			Status:   "Settled",
			Amount:   "0.001",
			Currency: "BTC",
		})
	}))
	defer server.Close()

	client := NewBTCPayClient(server.URL, "test-api-key", "store-456")
	invoice, err := client.GetInvoice(context.Background(), "inv-123")
	if err != nil {
		t.Fatalf("GetInvoice failed: %v", err)
	}
	if invoice.Status != "Settled" {
		t.Errorf("Status = %q, want Settled", invoice.Status)
	}
}

func TestBTCPayClient_GetInvoice_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewBTCPayClient(server.URL, "test-api-key", "store-456")
	_, err := client.GetInvoice(context.Background(), "nonexistent")
	if !errors.Is(err, domain.ErrInvoiceNotFound) {
		t.Errorf("expected ErrInvoiceNotFound, got %v", err)
	}
}

func TestBTCPayClient_ListInvoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]BTCPayInvoiceResponse{
			{ID: "inv-1", Status: "New"},
			{ID: "inv-2", Status: "Settled"},
		})
	}))
	defer server.Close()

	client := NewBTCPayClient(server.URL, "test-api-key", "store-456")
	invoices, err := client.ListInvoices(context.Background())
	if err != nil {
		t.Fatalf("ListInvoices failed: %v", err)
	}
	if len(invoices) != 2 {
		t.Errorf("expected 2 invoices, got %d", len(invoices))
	}
}

func TestBTCPayClient_CheckHealth(t *testing.T) {
	t.Run("healthy server", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/health" {
				t.Errorf("expected /api/v1/health, got %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"synchronized":true}`))
		}))
		defer server.Close()

		client := NewBTCPayClient(server.URL, "test-api-key", "store-456")
		if err := client.CheckHealth(context.Background()); err != nil {
			t.Errorf("expected nil error, got: %v", err)
		}
	})

	t.Run("unhealthy server", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		client := NewBTCPayClient(server.URL, "test-api-key", "store-456")
		if err := client.CheckHealth(context.Background()); err == nil {
			t.Error("expected error for unhealthy server, got nil")
		}
	})

	t.Run("unreachable server", func(t *testing.T) {
		client := NewBTCPayClient("http://localhost:59999", "test-api-key", "store-456")
		err := client.CheckHealth(context.Background())
		if err == nil {
			t.Error("expected error for unreachable server, got nil")
		}
		if !errors.Is(err, domain.ErrBTCPayUnavailable) {
			t.Errorf("expected ErrBTCPayUnavailable, got %v", err)
		}
	})
}

func TestBTCPayClient_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
	}))
	defer server.Close()

	client := NewBTCPayClient(server.URL, "test-api-key", "store-456")
	_, err := client.CreateInvoice(context.Background(), &CreateInvoiceRequest{
		Amount:   0.001,
		Currency: "BTC",
	})
	if err == nil {
		t.Error("expected error for server error, got nil")
	}
}
