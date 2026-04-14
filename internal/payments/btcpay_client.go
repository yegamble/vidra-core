package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"vidra-core/internal/domain"
)

// BTCPayClient communicates with a BTCPay Server instance via the Greenfield API v1.
type BTCPayClient struct {
	baseURL    string
	apiKey     string
	storeID    string
	httpClient *http.Client
}

// NewBTCPayClient creates a new BTCPay Greenfield API client.
func NewBTCPayClient(baseURL, apiKey, storeID string) *BTCPayClient {
	return &BTCPayClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		storeID: storeID,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// CreateInvoiceRequest is the request body for creating a BTCPay invoice.
type CreateInvoiceRequest struct {
	Amount   float64                `json:"amount"`
	Currency string                 `json:"currency"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
	Checkout *InvoiceCheckout       `json:"checkout,omitempty"`
}

// InvoiceCheckout configures checkout behavior.
type InvoiceCheckout struct {
	ExpirationMinutes int `json:"expirationMinutes,omitempty"`
}

// BTCPayInvoiceResponse is the response from BTCPay Server when creating or getting an invoice.
type BTCPayInvoiceResponse struct {
	ID           string                 `json:"id"`
	StoreID      string                 `json:"storeId"`
	Amount       string                 `json:"amount"`
	Currency     string                 `json:"currency"`
	Status       string                 `json:"status"`
	CheckoutLink string                 `json:"checkoutLink"`
	CreatedTime  int64                  `json:"createdTime"`
	ExpirationTime int64               `json:"expirationTime"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// CreateInvoice creates a new invoice on BTCPay Server.
func (c *BTCPayClient) CreateInvoice(ctx context.Context, req *CreateInvoiceRequest) (*BTCPayInvoiceResponse, error) {
	url := fmt.Sprintf("%s/api/v1/stores/%s/invoices", c.baseURL, c.storeID)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling invoice request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrBTCPayUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("BTCPay returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var invoice BTCPayInvoiceResponse
	if err := json.NewDecoder(resp.Body).Decode(&invoice); err != nil {
		return nil, fmt.Errorf("decoding invoice response: %w", err)
	}

	return &invoice, nil
}

// GetInvoice retrieves an invoice from BTCPay Server.
func (c *BTCPayClient) GetInvoice(ctx context.Context, invoiceID string) (*BTCPayInvoiceResponse, error) {
	url := fmt.Sprintf("%s/api/v1/stores/%s/invoices/%s", c.baseURL, c.storeID, invoiceID)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrBTCPayUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, domain.ErrInvoiceNotFound
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("BTCPay returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var invoice BTCPayInvoiceResponse
	if err := json.NewDecoder(resp.Body).Decode(&invoice); err != nil {
		return nil, fmt.Errorf("decoding invoice response: %w", err)
	}

	return &invoice, nil
}

// ListInvoices lists invoices from BTCPay Server.
func (c *BTCPayClient) ListInvoices(ctx context.Context) ([]BTCPayInvoiceResponse, error) {
	url := fmt.Sprintf("%s/api/v1/stores/%s/invoices", c.baseURL, c.storeID)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrBTCPayUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("BTCPay returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var invoices []BTCPayInvoiceResponse
	if err := json.NewDecoder(resp.Body).Decode(&invoices); err != nil {
		return nil, fmt.Errorf("decoding invoices response: %w", err)
	}

	return invoices, nil
}

// CheckHealth checks if BTCPay Server is reachable.
func (c *BTCPayClient) CheckHealth(ctx context.Context) error {
	url := fmt.Sprintf("%s/api/v1/health", c.baseURL)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating health request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrBTCPayUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("BTCPay health check returned HTTP %d", resp.StatusCode)
	}

	return nil
}

func (c *BTCPayClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "token "+c.apiKey)
	}
}
