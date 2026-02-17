package payments

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/crypto/blake2b"

	"athena/internal/domain"
)

var ErrNotImplemented = errors.New("not implemented: requires Programmable Transaction Block construction (Task 2)")

type IOTANodeClient interface {
	GetNodeInfo(ctx context.Context) (*NodeInfo, error)
	GetAddressBalance(ctx context.Context, address string) (int64, error)
	GetTransactionStatus(ctx context.Context, txHash string) (*TransactionStatus, error)
	SubmitTransaction(ctx context.Context, tx *SignedTransaction) (string, error)
}

type IOTAClient struct {
	nodeURL    string
	nodeClient IOTANodeClient
}

func NewIOTAClient(nodeURL string) *IOTAClient {
	return &IOTAClient{
		nodeURL: nodeURL,
		nodeClient: &jsonRPCClient{
			nodeURL:    nodeURL,
			httpClient: &http.Client{Timeout: 10 * time.Second},
			maxRetries: 3,
		},
	}
}

func NewIOTAClientWithMock(nodeClient IOTANodeClient) *IOTAClient {
	return &IOTAClient{
		nodeURL:    "mock://test",
		nodeClient: nodeClient,
	}
}

func (c *IOTAClient) GenerateKeypair() ([]byte, []byte, error) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate Ed25519 keypair: %w", err)
	}
	return privKey.Seed(), pubKey, nil
}

func (c *IOTAClient) DeriveAddress(publicKey []byte) (string, error) {
	if len(publicKey) == 0 {
		return "", fmt.Errorf("%w: public key cannot be empty", domain.ErrInvalidAddress)
	}

	input := make([]byte, 1+len(publicKey))
	input[0] = 0x00
	copy(input[1:], publicKey)

	hasher, err := blake2b.New256(nil)
	if err != nil {
		return "", fmt.Errorf("creating blake2b hasher: %w", err)
	}
	hasher.Write(input)
	addrBytes := hasher.Sum(nil)

	return "0x" + hex.EncodeToString(addrBytes), nil
}

func (c *IOTAClient) ValidateAddress(address string) bool {
	if len(address) != 66 {
		return false
	}
	if address[:2] != "0x" {
		return false
	}
	_, err := hex.DecodeString(address[2:])
	return err == nil
}

func (c *IOTAClient) GetBalance(ctx context.Context, address string) (int64, error) {
	balance, err := c.nodeClient.GetAddressBalance(ctx, address)
	if err != nil {
		if err == context.Canceled || err == context.DeadlineExceeded {
			return 0, err
		}
		return 0, fmt.Errorf("%w: %v", domain.ErrIOTANodeUnavailable, err)
	}
	return balance, nil
}

func (c *IOTAClient) GetTransactionStatus(ctx context.Context, txDigest string) (*TransactionStatus, error) {
	status, err := c.nodeClient.GetTransactionStatus(ctx, txDigest)
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction status: %w", err)
	}
	return status, nil
}

func (c *IOTAClient) GetNodeInfo(ctx context.Context) (*NodeInfo, error) {
	return c.nodeClient.GetNodeInfo(ctx)
}

func (c *IOTAClient) WaitForConfirmation(ctx context.Context, txDigest string, requiredConfirms int, pollInterval time.Duration) (int, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticker.C:
			status, err := c.GetTransactionStatus(ctx, txDigest)
			if err != nil {
				return 0, fmt.Errorf("failed to check transaction status: %w", err)
			}
			if status.Confirmations >= requiredConfirms {
				return status.Confirmations, nil
			}
		}
	}
}

// TODO Task 2 Phase 2: implement as Programmable Transaction Block for IOTA Rebased.
func (c *IOTAClient) BuildTransaction(fromAddress, toAddress string, amount int64) (*UnsignedTransaction, error) {
	return nil, fmt.Errorf("BuildTransaction: %w", ErrNotImplemented)
}

// TODO Task 2 Phase 2: implement Ed25519 signing for IOTA Rebased PTBs.
func (c *IOTAClient) SignTransaction(privateKeyHex string, tx interface{}) (*SignedTransaction, error) {
	return nil, fmt.Errorf("SignTransaction: %w", ErrNotImplemented)
}

// TODO Task 2 Phase 2: implement via JSON-RPC iota_executeTransactionBlock.
func (c *IOTAClient) SubmitTransaction(ctx context.Context, tx *SignedTransaction) (string, error) {
	return "", fmt.Errorf("SubmitTransaction: %w", ErrNotImplemented)
}

type jsonRPCClient struct {
	nodeURL    string
	httpClient *http.Client
	maxRetries int
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int           `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *jsonRPCClient) callRPC(ctx context.Context, method string, params []interface{}, result interface{}) error {
	reqBody := rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshaling RPC request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(1<<uint(attempt-1)) * 200 * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.nodeURL, bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("creating HTTP request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("server error: HTTP %d", resp.StatusCode)
			continue
		}

		var rpcResp rpcResponse
		if decErr := json.NewDecoder(resp.Body).Decode(&rpcResp); decErr != nil {
			resp.Body.Close()
			return fmt.Errorf("decoding RPC response: %w", decErr)
		}
		resp.Body.Close()

		if rpcResp.Error != nil {
			return fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
		}

		return json.Unmarshal(rpcResp.Result, result)
	}
	return fmt.Errorf("RPC call %q failed after %d attempts: %w", method, c.maxRetries+1, lastErr)
}

func (c *jsonRPCClient) GetNodeInfo(ctx context.Context) (*NodeInfo, error) {
	var seqStr string
	if err := c.callRPC(ctx, "iota_getLatestCheckpointSequenceNumber", nil, &seqStr); err != nil {
		return nil, fmt.Errorf("getting node info: %w", err)
	}
	return &NodeInfo{
		NetworkID: "iota",
		Version:   seqStr,
		IsHealthy: true,
	}, nil
}

type balanceResult struct {
	CoinType     string `json:"coinType"`
	TotalBalance string `json:"totalBalance"`
}

func (c *jsonRPCClient) GetAddressBalance(ctx context.Context, address string) (int64, error) {
	params := []interface{}{address, "0x2::iota::IOTA"}
	var result balanceResult
	if err := c.callRPC(ctx, "iotax_getBalance", params, &result); err != nil {
		return 0, fmt.Errorf("getting balance for %s: %w", address, err)
	}
	balance, err := strconv.ParseInt(result.TotalBalance, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing balance %q: %w", result.TotalBalance, err)
	}
	return balance, nil
}

type txBlockResult struct {
	Digest     string `json:"digest"`
	Checkpoint string `json:"checkpoint,omitempty"`
}

func (c *jsonRPCClient) GetTransactionStatus(ctx context.Context, txDigest string) (*TransactionStatus, error) {
	params := []interface{}{txDigest, map[string]bool{"showEffects": true}}
	var result txBlockResult
	if err := c.callRPC(ctx, "iota_getTransactionBlock", params, &result); err != nil {
		return nil, fmt.Errorf("getting transaction block %s: %w", txDigest, err)
	}

	confirmations := 0
	if result.Checkpoint != "" {
		confirmations = 1
	}
	return &TransactionStatus{
		TxHash:        result.Digest,
		Confirmations: confirmations,
		IsConfirmed:   confirmations > 0,
		BlockID:       result.Checkpoint,
	}, nil
}

// TODO Task 2 Phase 2: implement via iota_executeTransactionBlock.
func (c *jsonRPCClient) SubmitTransaction(ctx context.Context, tx *SignedTransaction) (string, error) {
	return "", fmt.Errorf("SubmitTransaction: %w", ErrNotImplemented)
}

type UnsignedTransaction struct {
	FromAddress string
	ToAddress   string
	Amount      int64
	Nonce       int64
}

type SignedTransaction struct {
	FromAddress string
	ToAddress   string
	Amount      int64
	Signature   []byte
	Nonce       int64
}

type TransactionStatus struct {
	TxHash        string
	Confirmations int
	IsConfirmed   bool
	BlockID       string
}

type NodeInfo struct {
	NetworkID string
	Version   string
	IsHealthy bool
}
