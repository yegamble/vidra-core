package payments

import (
	"net/http"
	"strconv"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/repository"
)

// WalletHandler exposes wallet balance + transaction history endpoints.
// Phase 8 Task 3 — see plan: docs/plans/2026-04-24-phase-8-bitcoin-btcpay-wiring-finish.md
type WalletHandler struct {
	ledger *repository.PaymentLedgerRepository
}

// NewWalletHandler constructs a new wallet handler.
func NewWalletHandler(ledger *repository.PaymentLedgerRepository) *WalletHandler {
	return &WalletHandler{ledger: ledger}
}

// GetBalance handles GET /api/v1/payments/wallet/balance.
//
// Response shape per plan F02:
//
//	available_sats:      SUM(amount_sats) WHERE user_id = current — spendable
//	pending_payout_sats: ABS(SUM(payout_requested negatives)) for pending|approved
//	                     payouts — informational only, NOT subtracted from
//	                     available_sats again.
func (h *WalletHandler) GetBalance(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Unauthorized"))
		return
	}

	available, err := h.ledger.GetAvailableBalance(r.Context(), nil, userID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("BALANCE_QUERY_FAILED", "Failed to read balance"))
		return
	}
	pending, err := h.ledger.GetPendingPayoutSats(r.Context(), userID)
	if err != nil {
		// Pending is informational; degrade gracefully when its query fails.
		pending = 0
	}

	shared.WriteJSON(w, http.StatusOK, domain.WalletBalance{
		AvailableSats:     available,
		PendingPayoutSats: pending,
		Currency:          "BTC",
		AsOf:              time.Now().UTC(),
	})
}

// GetTransactions handles GET /api/v1/payments/wallet/transactions.
//
// Query params:
//
//	direction:  "sent" | "received" | "all" (default all)
//	type:       any LedgerEntryType (filter)
//	start:      pagination offset (int, default 0)
//	count:      page size (int, default 20, max 100)
//	start_date: RFC3339 timestamp (>=)
//	end_date:   RFC3339 timestamp (<=)
//
// Response: { data: [...], total, start, count }
func (h *WalletHandler) GetTransactions(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Unauthorized"))
		return
	}

	q := r.URL.Query()
	dir := domain.LedgerDirection(q.Get("direction"))
	switch dir {
	case domain.LedgerDirectionSent, domain.LedgerDirectionReceived, domain.LedgerDirectionAll:
		// ok
	case "":
		dir = domain.LedgerDirectionAll
	default:
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_DIRECTION", "direction must be sent|received|all"))
		return
	}

	entryType := domain.LedgerEntryType(q.Get("type"))
	if entryType != "" && !entryType.IsValid() {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_TYPE", "Unknown ledger entry type"))
		return
	}

	start, _ := strconv.Atoi(q.Get("start"))
	count, _ := strconv.Atoi(q.Get("count"))
	if count <= 0 {
		count = 20
	}

	filter := domain.LedgerListFilter{
		UserID:    userID,
		Direction: dir,
		EntryType: entryType,
		Limit:     count,
		Offset:    start,
	}
	if s := q.Get("start_date"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			filter.StartDate = &t
		}
	}
	if s := q.Get("end_date"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			filter.EndDate = &t
		}
	}

	rows, total, err := h.ledger.ListEntries(r.Context(), filter)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("LIST_FAILED", "Failed to list transactions"))
		return
	}

	shared.WriteJSONWithMeta(w, http.StatusOK, rows, &shared.Meta{
		Total:  int64(total),
		Limit:  filter.Limit,
		Offset: filter.Offset,
	})
}
