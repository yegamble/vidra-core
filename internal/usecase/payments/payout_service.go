package payments

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"vidra-core/internal/domain"
	"vidra-core/internal/repository"
)

// MinPayoutSats is the floor for a payout. Configurable later via PaymentConfig
// (Phase 8 Task 12 frontend); for now it's a sensible default that exceeds the
// Bitcoin dust limit (546 sats for P2WPKH) and matches the plan's 50_000 default
// only on the *worker* side; the *minimum-allowed-amount* check below is the
// dust-floor, distinct from the worker's notification threshold.
const MinPayoutSats int64 = 546

// PayoutService is the business-logic layer for the payout state machine.
// All transitions are atomic (single conditional UPDATE) and emit notifications.
type PayoutService struct {
	payoutRepo  *repository.PayoutRepository
	ledgerRepo  *repository.PaymentLedgerRepository
	notifier    NotificationEmitter
	adminLister AdminLister
}

// AdminLister returns the user-IDs of all platform admins (for fan-out
// notifications when a payout enters 'pending').
type AdminLister interface {
	ListAdminIDs(ctx context.Context) ([]string, error)
}

// NewPayoutService wires the payout service.
func NewPayoutService(
	payoutRepo *repository.PayoutRepository,
	ledgerRepo *repository.PaymentLedgerRepository,
	notifier NotificationEmitter,
	adminLister AdminLister,
) *PayoutService {
	return &PayoutService{
		payoutRepo:  payoutRepo,
		ledgerRepo:  ledgerRepo,
		notifier:    notifier,
		adminLister: adminLister,
	}
}

// RequestPayout creates a pending payout + reservation ledger entry.
// Returns ErrInsufficientBalance when the user can't cover the amount.
func (s *PayoutService) RequestPayout(ctx context.Context, userID string, req domain.PayoutRequest) (*domain.Payout, error) {
	if req.AmountSats < MinPayoutSats {
		return nil, domain.ErrPayoutAmountTooSmall
	}
	if err := validateDestination(req.Destination, req.DestinationType); err != nil {
		return nil, err
	}

	// Snapshot balance — must be ≥ requested.
	bal, err := s.ledgerRepo.GetAvailableBalance(ctx, nil, userID)
	if err != nil {
		return nil, fmt.Errorf("balance check: %w", err)
	}
	if bal < req.AmountSats {
		return nil, domain.ErrInsufficientBalance
	}

	payout := &domain.Payout{
		RequesterUserID: userID,
		AmountSats:      req.AmountSats,
		Destination:     strings.TrimSpace(req.Destination),
		DestinationType: req.DestinationType,
		AutoTrigger:     req.AutoTrigger,
	}
	if err := s.payoutRepo.Create(ctx, nil, payout); err != nil {
		return nil, err
	}

	// Reservation ledger entry — ties to the new payout via payout_id.
	reservation := &domain.PaymentLedgerEntry{
		UserID:         userID,
		EntryType:      domain.LedgerEntryPayoutRequested,
		AmountSats:     -req.AmountSats,
		Currency:       "BTC",
		PayoutID:       nullString(payout.ID),
		IdempotencyKey: domain.PayoutRequestIdempotencyKey(payout.ID),
	}
	if err := s.ledgerRepo.Record(ctx, reservation); err != nil && !errors.Is(err, domain.ErrLedgerDuplicate) {
		// Best-effort rollback: cancel the just-created payout so balance is consistent.
		_ = s.payoutRepo.TransitionPending(ctx, payout.ID, domain.PayoutStatusCancelled, "", "ledger reservation failed")
		return nil, fmt.Errorf("reservation: %w", err)
	}

	// Fan-out notification to admins (best-effort).
	s.notifyAdmins(ctx, payout)
	return payout, nil
}

// CancelPayout — creator self-cancels a pending payout.
func (s *PayoutService) CancelPayout(ctx context.Context, payoutID, requesterID string) error {
	p, err := s.payoutRepo.GetByID(ctx, payoutID)
	if err != nil {
		return err
	}
	if p.RequesterUserID != requesterID {
		return domain.ErrPayoutForbidden
	}
	if err := s.payoutRepo.TransitionPending(ctx, payoutID, domain.PayoutStatusCancelled, "", ""); err != nil {
		return err
	}
	// Compensating ledger entry.
	if err := s.recordCompensation(ctx, p, domain.LedgerEntryPayoutCancelled, domain.PayoutCancelledIdempotencyKey(p.ID)); err != nil {
		slog.Error("payout cancel: ledger compensation failed", "payout_id", p.ID, "err", err)
	}
	return nil
}

// ApprovePayout — admin transitions pending → approved.
func (s *PayoutService) ApprovePayout(ctx context.Context, payoutID, adminID string) error {
	if err := s.payoutRepo.TransitionPending(ctx, payoutID, domain.PayoutStatusApproved, adminID, ""); err != nil {
		return err
	}
	if p, err := s.payoutRepo.GetByID(ctx, payoutID); err == nil && s.notifier != nil {
		_ = s.notifier.Emit(ctx, p.RequesterUserID, domain.NotificationPayoutApproved,
			"Payout approved",
			fmt.Sprintf("%d sats payout approved by admin", p.AmountSats),
			map[string]interface{}{"payout_id": p.ID})
	}
	return nil
}

// RejectPayout — admin transitions pending OR approved → rejected, restoring balance.
func (s *PayoutService) RejectPayout(ctx context.Context, payoutID, reason string) error {
	p, err := s.payoutRepo.GetByID(ctx, payoutID)
	if err != nil {
		return err
	}
	switch p.Status {
	case domain.PayoutStatusPending:
		if err := s.payoutRepo.TransitionPending(ctx, payoutID, domain.PayoutStatusRejected, "", reason); err != nil {
			return err
		}
	case domain.PayoutStatusApproved:
		if err := s.payoutRepo.TransitionApproved(ctx, payoutID, domain.PayoutStatusRejected, "", reason); err != nil {
			return err
		}
	default:
		return domain.ErrPayoutInvalidStatus
	}
	if err := s.recordCompensation(ctx, p, domain.LedgerEntryPayoutRejected, domain.PayoutRejectedIdempotencyKey(p.ID)); err != nil {
		slog.Error("payout reject: ledger compensation failed", "payout_id", p.ID, "err", err)
	}
	if s.notifier != nil {
		_ = s.notifier.Emit(ctx, p.RequesterUserID, domain.NotificationPayoutRejected,
			"Payout rejected",
			fmt.Sprintf("Payout rejected: %s", reason),
			map[string]interface{}{"payout_id": p.ID, "reason": reason})
	}
	return nil
}

// MarkExecuted — admin records ops-executed payout (approved → completed) with txid/LN hash.
func (s *PayoutService) MarkExecuted(ctx context.Context, payoutID, txid string) error {
	if strings.TrimSpace(txid) == "" {
		return domain.NewDomainError("MISSING_TXID", "txid (or LN payment hash) is required to mark executed")
	}
	if err := s.payoutRepo.TransitionApproved(ctx, payoutID, domain.PayoutStatusCompleted, txid, ""); err != nil {
		return err
	}
	p, err := s.payoutRepo.GetByID(ctx, payoutID)
	if err != nil {
		return err
	}
	// Record a marker (amount=0) ledger entry; the reservation negative remains permanent.
	marker := &domain.PaymentLedgerEntry{
		UserID:         p.RequesterUserID,
		EntryType:      domain.LedgerEntryPayoutCompleted,
		AmountSats:     0,
		Currency:       "BTC",
		PayoutID:       nullString(p.ID),
		IdempotencyKey: domain.PayoutCompletedIdempotencyKey(p.ID),
		Metadata:       []byte(fmt.Sprintf(`{"txid":%q}`, txid)),
	}
	if err := s.ledgerRepo.Record(ctx, marker); err != nil && !errors.Is(err, domain.ErrLedgerDuplicate) {
		slog.Error("payout completed: ledger marker failed", "payout_id", p.ID, "err", err)
	}
	if s.notifier != nil {
		_ = s.notifier.Emit(ctx, p.RequesterUserID, domain.NotificationPayoutCompleted,
			"Payout completed",
			fmt.Sprintf("%d sats payout sent (txid: %s)", p.AmountSats, txid),
			map[string]interface{}{"payout_id": p.ID, "txid": txid})
	}
	return nil
}

// ListMine — paginated payouts for the current user.
func (s *PayoutService) ListMine(ctx context.Context, userID string, limit, offset int) ([]*domain.Payout, int, error) {
	return s.payoutRepo.ListMine(ctx, userID, limit, offset)
}

// ListPending — admin queue.
func (s *PayoutService) ListPending(ctx context.Context, limit, offset int) ([]*domain.Payout, int, error) {
	return s.payoutRepo.ListPending(ctx, limit, offset)
}

// recordCompensation writes a positive ledger entry that restores the reserved
// amount when a payout is rejected or cancelled. Idempotent via per-payout key.
func (s *PayoutService) recordCompensation(ctx context.Context, p *domain.Payout, et domain.LedgerEntryType, key string) error {
	entry := &domain.PaymentLedgerEntry{
		UserID:         p.RequesterUserID,
		EntryType:      et,
		AmountSats:     p.AmountSats, // positive — restores balance
		Currency:       "BTC",
		PayoutID:       nullString(p.ID),
		IdempotencyKey: key,
	}
	if err := s.ledgerRepo.Record(ctx, entry); err != nil && !errors.Is(err, domain.ErrLedgerDuplicate) {
		return err
	}
	return nil
}

// notifyAdmins fans out a payout_pending_approval notification to every admin.
// Failures are logged but never block the request.
func (s *PayoutService) notifyAdmins(ctx context.Context, p *domain.Payout) {
	if s.notifier == nil || s.adminLister == nil {
		return
	}
	admins, err := s.adminLister.ListAdminIDs(ctx)
	if err != nil {
		slog.Warn("payout request: admin lookup failed", "err", err)
		return
	}
	title := "Payout request"
	msg := fmt.Sprintf("New payout request: %d sats (%s)", p.AmountSats, p.DestinationType)
	for _, aid := range admins {
		if err := s.notifier.Emit(ctx, aid, domain.NotificationPayoutPendingApproval,
			title, msg, map[string]interface{}{"payout_id": p.ID, "amount_sats": p.AmountSats}); err != nil {
			slog.Warn("payout admin notify failed", "admin", aid, "err", err)
		}
	}
}

// validateDestination performs a lightweight prefix sanity check. Backend is the
// source of truth for full validation (bech32 checksum etc.) — done at the BTCPay
// layer in Task 5; here we only block obvious garbage.
func validateDestination(dest string, dtype domain.PayoutDestinationType) error {
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return domain.ErrPayoutInvalidDest
	}
	switch dtype {
	case domain.PayoutDestOnChain:
		// regtest, testnet, mainnet shapes
		ok := strings.HasPrefix(dest, "bc1") ||
			strings.HasPrefix(dest, "tb1") ||
			strings.HasPrefix(dest, "bcrt1") ||
			strings.HasPrefix(dest, "1") ||
			strings.HasPrefix(dest, "2") ||
			strings.HasPrefix(dest, "3") ||
			strings.HasPrefix(dest, "m") ||
			strings.HasPrefix(dest, "n")
		if !ok {
			return domain.ErrPayoutInvalidDest
		}
	case domain.PayoutDestLightning:
		l := strings.ToLower(dest)
		if !strings.HasPrefix(l, "lnbc") && !strings.HasPrefix(l, "lntb") && !strings.HasPrefix(l, "lnbcrt") {
			return domain.ErrPayoutInvalidDest
		}
	default:
		return domain.ErrPayoutInvalidDest
	}
	return nil
}

