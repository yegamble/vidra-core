// Package main implements a lightweight mock IOTA JSON-RPC 2.0 server for integration testing.
// It responds to the IOTA node RPC methods used by the test connection handler and payments code.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      interface{}   `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func handleRPC(req rpcRequest) rpcResponse {
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	switch req.Method {
	case "iota_getChainIdentifier":
		resp.Result = "35834a8a"

	case "iota_getLatestCheckpointSequenceNumber":
		resp.Result = "1000"

	case "iotax_getBalance":
		resp.Result = map[string]interface{}{
			"coinType":     "0x2::iota::IOTA",
			"totalBalance": "1000000000",
		}

	case "iota_getTransactionBlock":
		resp.Result = map[string]interface{}{
			"digest": "FAKE_TX_DIGEST",
			"transaction": map[string]interface{}{
				"data": map[string]interface{}{
					"messageVersion": "v1",
					"transaction": map[string]interface{}{
						"kind":       "ProgrammableTransaction",
						"sender":     "0x1234",
						"gasData":    map[string]interface{}{},
						"expiration": nil,
					},
					"sender": "0x1234",
					"gasData": map[string]interface{}{
						"payment": []interface{}{},
						"owner":   "0x1234",
						"price":   "1000",
						"budget":  "10000000",
					},
				},
			},
			"effects": map[string]interface{}{
				"status": map[string]interface{}{
					"status": "success",
				},
			},
		}

	case "iota_executeTransactionBlock":
		resp.Result = map[string]interface{}{
			"digest": "EXECUTED_TX_DIGEST",
			"effects": map[string]interface{}{
				"status": map[string]interface{}{
					"status": "success",
				},
			},
		}

	case "iota_dryRunTransactionBlock":
		resp.Result = map[string]interface{}{
			"effects": map[string]interface{}{
				"status": map[string]interface{}{
					"status": "success",
				},
			},
		}

	case "iotax_queryTransactionBlocks":
		// Extract ToAddress from the request filter so balance change owner matches
		addr := "0x1234"
		if len(req.Params) > 0 {
			if p, ok := req.Params[0].(map[string]interface{}); ok {
				if f, ok := p["filter"].(map[string]interface{}); ok {
					if a, ok := f["ToAddress"].(string); ok {
						addr = a
					}
				}
			}
		}
		resp.Result = map[string]interface{}{
			"data": []map[string]interface{}{
				{
					"digest":      "MOCK_TX_DIGEST_001",
					"timestampMs": fmt.Sprintf("%d", time.Now().UnixMilli()),
					"balanceChanges": []map[string]interface{}{
						{
							"owner":    map[string]string{"AddressOwner": addr},
							"coinType": "0x2::iota::IOTA",
							"amount":   "1000000000",
						},
					},
				},
			},
			"nextCursor":  nil,
			"hasNextPage": false,
		}

	default:
		resp.Error = rpcError{
			Code:    -32601,
			Message: "Method not found",
		}
	}

	return resp
}

func newRouter() http.Handler {
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// JSON-RPC endpoint — all RPC calls go to root (same as real IOTA node)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req rpcRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 65536)).Decode(&req); err != nil {
			writeJSON(w, rpcResponse{
				JSONRPC: "2.0",
				ID:      nil,
				Error: rpcError{
					Code:    -32700,
					Message: "Parse error",
				},
			})
			return
		}

		resp := handleRPC(req)
		writeJSON(w, resp)
	})

	return mux
}

func main() {
	port := "8080"
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      newRouter(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	log.Printf("Mock IOTA JSON-RPC server listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
