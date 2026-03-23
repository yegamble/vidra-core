package payments

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/crypto/blake2b"

	"athena/internal/domain"
)

const (
	// DefaultGasBudget is the default gas budget for simple IOTA transfers (0.01 IOTA in nanos).
	DefaultGasBudget = 10_000_000

	// ed25519Flag identifies the Ed25519 signature scheme in IOTA Rebased.
	ed25519Flag = 0x00
)

// intentPrefix is the 3-byte intent prefix for IOTA transaction signing:
// [IntentScope::TransactionData(0), IntentVersion::V0(0), AppId::Iota(0)].
var intentPrefix = []byte{0x00, 0x00, 0x00}

type IOTANodeClient interface {
	GetNodeInfo(ctx context.Context) (*NodeInfo, error)
	GetAddressBalance(ctx context.Context, address string) (int64, error)
	GetTransactionStatus(ctx context.Context, txHash string) (*TransactionStatus, error)
	SubmitTransaction(ctx context.Context, tx *SignedTransaction) (string, error)
}

// TransactionBuilder extends IOTANodeClient with IOTA Rebased transaction building
// capabilities. The jsonRPCClient implements this interface; mock clients typically don't.
type TransactionBuilder interface {
	GetCoins(ctx context.Context, owner string) ([]CoinObject, error)
	PayIota(ctx context.Context, signer string, inputCoins []string, recipients []string, amounts []string, gasBudget string) ([]byte, error)
	ExecuteTransactionBlock(ctx context.Context, txBytes string, signatures []string) (string, error)
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

func (c *IOTAClient) GetNodeStatus(ctx context.Context) error {
	info, err := c.nodeClient.GetNodeInfo(ctx)
	if err != nil {
		return err
	}
	if !info.IsHealthy {
		return fmt.Errorf("IOTA node is not healthy")
	}
	return nil
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

// BuildTransaction constructs an unsigned IOTA Rebased transaction using the node's
// unsafe_payIota JSON-RPC method. The node returns BCS-serialized transaction bytes
// which can then be signed locally and submitted.
func (c *IOTAClient) BuildTransaction(ctx context.Context, fromAddress, toAddress string, amount int64) (*UnsignedTransaction, error) {
	builder, ok := c.nodeClient.(TransactionBuilder)
	if !ok {
		return nil, fmt.Errorf("BuildTransaction: node client does not support transaction building")
	}

	if !c.ValidateAddress(fromAddress) {
		return nil, fmt.Errorf("BuildTransaction: %w: invalid sender address", domain.ErrInvalidAddress)
	}
	if !c.ValidateAddress(toAddress) {
		return nil, fmt.Errorf("BuildTransaction: %w: invalid recipient address", domain.ErrInvalidAddress)
	}
	if amount <= 0 {
		return nil, fmt.Errorf("BuildTransaction: %w: amount must be positive", domain.ErrBadRequest)
	}

	coins, err := builder.GetCoins(ctx, fromAddress)
	if err != nil {
		return nil, fmt.Errorf("BuildTransaction: getting coins: %w", err)
	}
	if len(coins) == 0 {
		return nil, fmt.Errorf("BuildTransaction: %w: no coins found for address %s", domain.ErrInsufficientBalance, fromAddress)
	}

	coinIDs := make([]string, len(coins))
	for i, coin := range coins {
		coinIDs[i] = coin.CoinObjectID
	}

	amountStr := strconv.FormatInt(amount, 10)
	gasBudgetStr := strconv.FormatInt(DefaultGasBudget, 10)

	txBytes, err := builder.PayIota(ctx, fromAddress, coinIDs, []string{toAddress}, []string{amountStr}, gasBudgetStr)
	if err != nil {
		return nil, fmt.Errorf("BuildTransaction: building payment: %w", err)
	}

	return &UnsignedTransaction{
		FromAddress: fromAddress,
		ToAddress:   toAddress,
		Amount:      amount,
		TxBytes:     txBytes,
	}, nil
}

// SignTransaction signs an unsigned IOTA Rebased transaction using Ed25519.
// The signing follows the IOTA Rebased protocol:
//  1. Construct intent message: intentPrefix (3 bytes) || BCS transaction bytes
//  2. Blake2b-256 hash the intent message
//  3. Ed25519 sign the hash
//  4. Produce signature: ed25519Flag (1 byte) || signature (64 bytes) || publicKey (32 bytes)
func (c *IOTAClient) SignTransaction(privateKeyHex string, tx *UnsignedTransaction) (*SignedTransaction, error) {
	if tx == nil {
		return nil, fmt.Errorf("SignTransaction: transaction is nil")
	}
	if len(tx.TxBytes) == 0 {
		return nil, fmt.Errorf("SignTransaction: transaction bytes are empty")
	}

	privKeyBytes, err := hex.DecodeString(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("SignTransaction: decoding private key: %w", err)
	}
	if len(privKeyBytes) != ed25519.SeedSize {
		return nil, fmt.Errorf("SignTransaction: invalid private key length: expected %d bytes, got %d", ed25519.SeedSize, len(privKeyBytes))
	}

	// Construct intent message: intent_prefix || tx_bytes
	intentMsg := make([]byte, len(intentPrefix)+len(tx.TxBytes))
	copy(intentMsg, intentPrefix)
	copy(intentMsg[len(intentPrefix):], tx.TxBytes)

	// Blake2b-256 hash
	hasher, err := blake2b.New256(nil)
	if err != nil {
		return nil, fmt.Errorf("SignTransaction: creating blake2b hasher: %w", err)
	}
	hasher.Write(intentMsg)
	digest := hasher.Sum(nil)

	// Ed25519 sign
	privKey := ed25519.NewKeyFromSeed(privKeyBytes)
	signature := ed25519.Sign(privKey, digest)
	pubKey := privKey.Public().(ed25519.PublicKey)

	// Construct IOTA signature: flag(1) || signature(64) || pubkey(32)
	iotaSig := make([]byte, 1+ed25519.SignatureSize+ed25519.PublicKeySize)
	iotaSig[0] = ed25519Flag
	copy(iotaSig[1:], signature)
	copy(iotaSig[1+ed25519.SignatureSize:], pubKey)

	return &SignedTransaction{
		FromAddress: tx.FromAddress,
		ToAddress:   tx.ToAddress,
		Amount:      tx.Amount,
		TxBytes:     tx.TxBytes,
		Signature:   iotaSig,
	}, nil
}

// SubmitTransaction submits a signed transaction to the IOTA Rebased network
// via the iota_executeTransactionBlock JSON-RPC method.
func (c *IOTAClient) SubmitTransaction(ctx context.Context, tx *SignedTransaction) (string, error) {
	if tx == nil {
		return "", fmt.Errorf("SubmitTransaction: transaction is nil")
	}
	if len(tx.TxBytes) == 0 {
		return "", fmt.Errorf("SubmitTransaction: transaction bytes are empty")
	}
	if len(tx.Signature) == 0 {
		return "", fmt.Errorf("SubmitTransaction: signature is empty")
	}

	builder, ok := c.nodeClient.(TransactionBuilder)
	if !ok {
		// Fall back to the IOTANodeClient.SubmitTransaction for mock testing
		return c.nodeClient.SubmitTransaction(ctx, tx)
	}

	txBytesB64 := base64.StdEncoding.EncodeToString(tx.TxBytes)
	sigB64 := base64.StdEncoding.EncodeToString(tx.Signature)

	digest, err := builder.ExecuteTransactionBlock(ctx, txBytesB64, []string{sigB64})
	if err != nil {
		return "", fmt.Errorf("SubmitTransaction: %w", err)
	}

	return digest, nil
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

// SubmitTransaction submits a signed transaction via iota_executeTransactionBlock.
func (c *jsonRPCClient) SubmitTransaction(ctx context.Context, tx *SignedTransaction) (string, error) {
	if len(tx.TxBytes) == 0 || len(tx.Signature) == 0 {
		return "", fmt.Errorf("SubmitTransaction: transaction bytes and signature are required")
	}

	txBytesB64 := base64.StdEncoding.EncodeToString(tx.TxBytes)
	sigB64 := base64.StdEncoding.EncodeToString(tx.Signature)

	return c.ExecuteTransactionBlock(ctx, txBytesB64, []string{sigB64})
}

// GetCoins retrieves IOTA coin objects owned by the given address via iotax_getCoins.
func (c *jsonRPCClient) GetCoins(ctx context.Context, owner string) ([]CoinObject, error) {
	params := []interface{}{owner, "0x2::iota::IOTA"}
	var result coinsResponse
	if err := c.callRPC(ctx, "iotax_getCoins", params, &result); err != nil {
		return nil, fmt.Errorf("getting coins for %s: %w", owner, err)
	}
	return result.Data, nil
}

// PayIota builds a transaction via unsafe_payIota JSON-RPC. The node handles coin
// merging, splitting, and gas deduction. Returns the BCS-serialized transaction bytes.
func (c *jsonRPCClient) PayIota(ctx context.Context, signer string, inputCoins []string, recipients []string, amounts []string, gasBudget string) ([]byte, error) {
	params := []interface{}{signer, inputCoins, recipients, amounts, gasBudget}
	var result payIotaResponse
	if err := c.callRPC(ctx, "unsafe_payIota", params, &result); err != nil {
		return nil, fmt.Errorf("building payIota transaction: %w", err)
	}
	txBytes, err := base64.StdEncoding.DecodeString(result.TxBytes)
	if err != nil {
		return nil, fmt.Errorf("decoding transaction bytes: %w", err)
	}
	return txBytes, nil
}

// ExecuteTransactionBlock submits a signed transaction via iota_executeTransactionBlock.
func (c *jsonRPCClient) ExecuteTransactionBlock(ctx context.Context, txBytes string, signatures []string) (string, error) {
	params := []interface{}{
		txBytes,
		signatures,
		map[string]bool{"showEffects": true},
		"WaitForLocalExecution",
	}
	var result executeResponse
	if err := c.callRPC(ctx, "iota_executeTransactionBlock", params, &result); err != nil {
		return "", fmt.Errorf("executing transaction block: %w", err)
	}
	return result.Digest, nil
}

// CoinObject represents an IOTA coin object returned by iotax_getCoins.
type CoinObject struct {
	CoinObjectID string `json:"coinObjectId"`
	Version      string `json:"version"`
	Digest       string `json:"digest"`
	Balance      string `json:"balance"`
}

type coinsResponse struct {
	Data        []CoinObject `json:"data"`
	NextCursor  *string      `json:"nextCursor"`
	HasNextPage bool         `json:"hasNextPage"`
}

type payIotaResponse struct {
	TxBytes string `json:"txBytes"`
}

type executeResponse struct {
	Digest string `json:"digest"`
}

type UnsignedTransaction struct {
	FromAddress string
	ToAddress   string
	Amount      int64
	Nonce       int64
	TxBytes     []byte // BCS-serialized transaction data from the IOTA node
}

type SignedTransaction struct {
	FromAddress string
	ToAddress   string
	Amount      int64
	Signature   []byte // IOTA signature: flag(1) || ed25519_sig(64) || pubkey(32)
	Nonce       int64
	TxBytes     []byte // BCS-serialized transaction data
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
