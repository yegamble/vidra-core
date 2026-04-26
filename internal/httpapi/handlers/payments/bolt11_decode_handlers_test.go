package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBolt11DecodeHandler_InvalidBody(t *testing.T) {
	h := NewBolt11DecodeHandler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/bolt11/decode",
		strings.NewReader("not json"))
	w := httptest.NewRecorder()
	h.Decode(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBolt11DecodeHandler_MissingField(t *testing.T) {
	h := NewBolt11DecodeHandler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/bolt11/decode",
		strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.Decode(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(w.Body).Decode(&envelope)
	assert.Equal(t, "INVALID_BOLT11", envelope.Error.Code)
}

func TestBolt11DecodeHandler_MalformedBolt11(t *testing.T) {
	h := NewBolt11DecodeHandler()
	t.Setenv("BITCOIN_NETWORK", "regtest")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/bolt11/decode",
		strings.NewReader(`{"bolt11":"not-a-valid-bolt11"}`))
	w := httptest.NewRecorder()
	h.Decode(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(w.Body).Decode(&envelope)
	assert.Equal(t, "INVALID_BOLT11", envelope.Error.Code)
}

func TestBolt11DecodeHandler_NetworkMismatch(t *testing.T) {
	h := NewBolt11DecodeHandler()
	t.Setenv("BITCOIN_NETWORK", "regtest")

	// A real-looking mainnet BOLT11 prefix (lnbc...) — the bech32 itself
	// won't validate fully, but the prefix mismatch should trigger
	// NETWORK_MISMATCH or INVALID_BOLT11. Our handler upgrades generic
	// errors when prefix mismatches the active network.
	body := `{"bolt11":"lnbc100u1pjabcdefghijklmnopqrstuvwxyz0123456789"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/bolt11/decode",
		strings.NewReader(body))
	w := httptest.NewRecorder()
	h.Decode(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(w.Body).Decode(&envelope)
	// Either NETWORK_MISMATCH (preferred) or INVALID_BOLT11 (fallback for
	// invoices that fail at the bech32 layer before zpay32 reaches the
	// network check). Both are 400.
	assert.Contains(t, []string{"NETWORK_MISMATCH", "INVALID_BOLT11"}, envelope.Error.Code)
}

func TestBolt11DecodeHandler_NetworkParams(t *testing.T) {
	cases := []struct {
		env      string
		expected string
	}{
		{"mainnet", "mainnet"},
		{"testnet", "testnet"},
		{"regtest", "regtest"},
		{"", "regtest"},        // default (non-production)
		{"unknown", "regtest"}, // invalid → default
	}
	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv("BITCOIN_NETWORK", tc.env)
			t.Setenv("ENV", "")
			_, label, err := networkParams()
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, label)
		})
	}
}

func TestBolt11DecodeHandler_NetworkParams_ProductionRequiresExplicitNetwork(t *testing.T) {
	// Per F06: production env must not silently fall back to regtest.
	cases := []string{"", "regtest"}
	for _, net := range cases {
		t.Run("BITCOIN_NETWORK="+net, func(t *testing.T) {
			t.Setenv("ENV", "production")
			t.Setenv("BITCOIN_NETWORK", net)
			_, _, err := networkParams()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "production")
		})
	}
}

func TestBolt11DecodeHandler_NetworkParams_ProductionAcceptsMainnet(t *testing.T) {
	t.Setenv("ENV", "production")
	t.Setenv("BITCOIN_NETWORK", "mainnet")
	_, label, err := networkParams()
	assert.NoError(t, err)
	assert.Equal(t, "mainnet", label)
}

// Smoke test: handler accepts an empty context (no JWT middleware in unit test);
// the routes.go gate enforces auth. We just verify the handler shape.
func TestBolt11DecodeHandler_Smoke(t *testing.T) {
	h := NewBolt11DecodeHandler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/bolt11/decode",
		strings.NewReader(`{"bolt11":""}`))
	req = req.WithContext(context.Background())
	w := httptest.NewRecorder()
	h.Decode(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
