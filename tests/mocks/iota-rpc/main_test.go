package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer() *httptest.Server {
	return httptest.NewServer(newRouter())
}

func callRPC(t *testing.T, ts *httptest.Server, method string) map[string]interface{} {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":[]}`
	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s failed: %v", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for %s, got %d", method, resp.StatusCode)
	}
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return result
}

func TestHealthEndpoint(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestGetChainIdentifier(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	result := callRPC(t, ts, "iota_getChainIdentifier")
	if result["result"] == nil {
		t.Errorf("expected non-nil result, got %v", result)
	}
	if result["error"] != nil {
		t.Errorf("unexpected error: %v", result["error"])
	}
	// Chain identifier should be a hex string
	if chainID, ok := result["result"].(string); !ok || len(chainID) == 0 {
		t.Errorf("expected non-empty string chain identifier, got %v", result["result"])
	}
}

func TestGetLatestCheckpointSequenceNumber(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	result := callRPC(t, ts, "iota_getLatestCheckpointSequenceNumber")
	if result["result"] == nil {
		t.Errorf("expected non-nil result, got %v", result)
	}
	if result["error"] != nil {
		t.Errorf("unexpected error: %v", result["error"])
	}
}

func TestGetBalance(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"iotax_getBalance","params":["0x1234","0x2::iota::IOTA"]}`
	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST iotax_getBalance failed: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if result["result"] == nil {
		t.Errorf("expected non-nil result, got %v", result)
	}
	balanceResult, ok := result["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected object result, got %T: %v", result["result"], result["result"])
	}
	if _, ok := balanceResult["totalBalance"]; !ok {
		t.Error("iotax_getBalance result missing totalBalance")
	}
}

func TestUnknownMethod(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	result := callRPC(t, ts, "iota_nonExistentMethod")
	if result["error"] == nil {
		t.Error("expected error for unknown method, got nil")
	}
	errObj, ok := result["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected error object, got %T", result["error"])
	}
	// JSON-RPC Method not found code is -32601
	code, _ := errObj["code"].(float64)
	if code != -32601 {
		t.Errorf("expected error code -32601, got %v", errObj["code"])
	}
}

func TestGetTransactionBlock(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"iota_getTransactionBlock","params":["FAKE_TX_DIGEST"]}`
	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST iota_getTransactionBlock failed: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if result["result"] == nil {
		t.Errorf("expected non-nil result, got %v", result)
	}
}

func TestRPCPreservesID(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	body := `{"jsonrpc":"2.0","id":42,"method":"iota_getChainIdentifier","params":[]}`
	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	// id should be preserved
	id, _ := result["id"].(float64)
	if id != 42 {
		t.Errorf("expected id=42, got %v", result["id"])
	}
}
