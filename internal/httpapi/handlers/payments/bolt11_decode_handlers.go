package payments

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/lightningnetwork/lnd/zpay32"
)

// Bolt11DecodeHandler decodes BOLT11 Lightning invoices for the
// PayoutRequestDialog UI to show pre-submit details (amount, description,
// expiry). The endpoint is auth-gated but NOT admin-gated — any authed user
// can decode (no payment side-effect; pure structural validation).
//
// Network selection: defers to the BITCOIN_NETWORK env var (regtest, testnet,
// mainnet). Falls back to regtest in dev. A BOLT11 from a different network
// is rejected with 400 network_mismatch.
type Bolt11DecodeHandler struct{}

// NewBolt11DecodeHandler creates a new handler.
func NewBolt11DecodeHandler() *Bolt11DecodeHandler {
	return &Bolt11DecodeHandler{}
}

type bolt11DecodeRequest struct {
	Bolt11 string `json:"bolt11"`
}

type bolt11DecodeResponse struct {
	AmountSats        int64  `json:"amount_sats"`
	Description       string `json:"description"`
	ExpiresAt         string `json:"expires_at"`
	DestinationPubkey string `json:"destination_pubkey"`
	Network           string `json:"network"`
	Expired           bool   `json:"expired"`
}

func networkParams() (*chaincfg.Params, string) {
	switch strings.ToLower(os.Getenv("BITCOIN_NETWORK")) {
	case "mainnet":
		return &chaincfg.MainNetParams, "mainnet"
	case "testnet":
		return &chaincfg.TestNet3Params, "testnet"
	case "regtest", "":
		return &chaincfg.RegressionNetParams, "regtest"
	default:
		return &chaincfg.RegressionNetParams, "regtest"
	}
}

// Decode handles POST /api/v1/payments/bolt11/decode
func (h *Bolt11DecodeHandler) Decode(w http.ResponseWriter, r *http.Request) {
	var req bolt11DecodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "Invalid request body"))
		return
	}
	bolt11 := strings.TrimSpace(req.Bolt11)
	if bolt11 == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_BOLT11", "Missing bolt11 field"))
		return
	}

	params, networkLabel := networkParams()
	inv, err := zpay32.Decode(bolt11, params)
	if err != nil {
		// Try to detect the more specific "wrong network" error by matching
		// the error string. zpay32 returns "invoice not for current active
		// network" for cross-network attempts.
		msg := err.Error()
		if strings.Contains(msg, "not for current active network") ||
			strings.Contains(msg, "invalid bech32 string") && hasNetworkPrefix(bolt11, params) {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("NETWORK_MISMATCH", fmt.Sprintf("Invoice is not for the %s network", networkLabel)))
			return
		}
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_BOLT11", fmt.Sprintf("Failed to decode BOLT11: %s", msg)))
		return
	}

	var amountSats int64
	if inv.MilliSat != nil {
		amountSats = int64(inv.MilliSat.ToSatoshis())
	}
	desc := ""
	if inv.Description != nil {
		desc = *inv.Description
	}
	expiry := inv.Timestamp.Add(inv.Expiry())
	pubkey := ""
	if inv.Destination != nil {
		pubkey = hex.EncodeToString(inv.Destination.SerializeCompressed())
	}

	resp := bolt11DecodeResponse{
		AmountSats:        amountSats,
		Description:       desc,
		ExpiresAt:         expiry.UTC().Format(time.RFC3339),
		DestinationPubkey: pubkey,
		Network:           networkLabel,
		Expired:           time.Now().After(expiry),
	}
	shared.WriteJSON(w, http.StatusOK, resp)
}

// hasNetworkPrefix returns true when the bolt11 string starts with a known
// network HRP that doesn't match the active params. Used to upgrade a
// generic "invalid bech32" error into the more specific NETWORK_MISMATCH.
func hasNetworkPrefix(bolt11 string, active *chaincfg.Params) bool {
	lo := strings.ToLower(bolt11)
	prefixes := []string{"lnbc", "lntb", "lnbcrt"}
	matched := ""
	for _, p := range prefixes {
		if strings.HasPrefix(lo, p) {
			matched = p
			break
		}
	}
	if matched == "" {
		return false
	}
	switch matched {
	case "lnbc":
		return active.Name != "mainnet"
	case "lntb":
		return active.Name != "testnet3"
	case "lnbcrt":
		return active.Name != "regtest"
	}
	return false
}
