package auth

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Instance-ownership transfer (0131 + the A16 ruling). The owner marker was
// written in exactly one place — the first-run claim CTE in ownerclaim.go — and
// read by the guards that stop another administrator demoting, deactivating or
// deleting the person who installed the instance. Nothing moved it. An owner who
// left therefore had two choices: keep an account they no longer wanted, or hand
// the instance to nobody and leave `vidra doctor` telling the next operator to
// write an UPDATE by hand. This is the route out, and it lives beside the claim
// because those two are now the only writers of `users.is_owner`.

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotInstanceOwner means the caller is an administrator but not THE
	// instance owner. Vidra has no owner ROLE, so requireRole cannot express
	// this and the check has to live in the handler's service call.
	ErrNotInstanceOwner = errors.New("auth: only the instance owner can transfer ownership")
	// ErrOwnerTargetNotFound means no account matches the requested new owner.
	ErrOwnerTargetNotFound = errors.New("auth: no such account")
	// ErrOwnerTargetIneligible means the account exists but cannot hold the
	// marker: it is not an administrator, it is deactivated, it is a tombstone,
	// or it is the current owner asking to hand ownership to themselves. The
	// marker must always sit on an account that can actually sign in and reach
	// the console, or the transfer manufactures the unowned instance it exists
	// to prevent.
	ErrOwnerTargetIneligible = errors.New("auth: the account cannot be made instance owner")
	// ErrOwnerTransferConflict means another transfer committed first and the
	// single-owner index refused this one. Nothing changed; the caller re-reads
	// the owner and decides again.
	ErrOwnerTransferConflict = errors.New("auth: another ownership transfer completed first")
)

// OwnerTransfer is the outcome of a completed transfer, for the audit ledger and
// the two notices. FormerOwner is the caller — they keep their admin role and
// lose only the marker.
type OwnerTransfer struct {
	FormerOwnerID       uuid.UUID
	FormerOwnerUsername string
	NewOwnerID          uuid.UUID
	NewOwnerUsername    string
	// PreviousOwnersCleared is how many rows held the marker before this write.
	// Normally 1. Zero means the instance was UNMARKED — a pre-0131 upgrade the
	// backfill could not resolve — and the transfer is how it acquires an owner;
	// the marker cannot be 2 because the partial unique index refuses it.
	PreviousOwnersCleared int64
}

// TransferOwnership moves the instance-owner marker from the caller to targetID.
//
// The caller must BE the owner (an ordinary administrator is refused: the whole
// point of the marker is that other admins cannot dispose of it) and must
// re-enter their password, the same confirmation the account-closing and
// password-change routes ask for — this is the one action that permanently
// removes a capability from the caller.
//
// The target must be an active, non-tombstoned administrator, checked here for
// the readable error and re-asserted inside the write for the race. The former
// owner stays an administrator: this transfers the marker, not the role, and
// demoting them silently would be a second decision nobody asked for.
//
// Both parties are mailed, best-effort, by the rule the rest of this package
// follows — a relay that is down must not roll back a completed transfer.
func (s *Service) TransferOwnership(ctx context.Context, callerID, targetID uuid.UUID, password string) (OwnerTransfer, error) {
	caller, err := s.UserByID(ctx, callerID)
	if err != nil {
		return OwnerTransfer{}, err
	}
	// Standing before password, matching the last-admin refusal on
	// /auth/me/deactivate: the caller can read their own owner flag from
	// /auth/me, so refusing here discloses nothing, and there is no point asking
	// for a password to authorize an action that cannot proceed.
	if !caller.IsOwner {
		return OwnerTransfer{}, ErrNotInstanceOwner
	}
	if err := s.ConfirmPassword(ctx, callerID, password); err != nil {
		return OwnerTransfer{}, err
	}
	// Read the raw row rather than UserByID: a deactivated or tombstoned target
	// must be reported as INELIGIBLE, not as "no such account", because the
	// caller can see it in the admin user list and a 404 would read as a bug.
	target, err := s.repo.GetUserByID(ctx, targetID)
	if err != nil {
		return OwnerTransfer{}, ErrOwnerTargetNotFound
	}
	if target.ID == caller.ID || target.Role != "admin" || !target.IsActive || target.DeletedAt.Valid {
		return OwnerTransfer{}, ErrOwnerTargetIneligible
	}
	row, err := s.repo.TransferInstanceOwner(ctx, targetID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The target stopped being eligible between the read above and the
		// write. Nothing was cleared — the statement's clear carries the same
		// test — so the instance still has its owner.
		return OwnerTransfer{}, ErrOwnerTargetIneligible
	case pgconv.IsUniqueViolation(err):
		// users_single_owner_idx refused a second marker: another transfer
		// committed while this one was in flight.
		return OwnerTransfer{}, ErrOwnerTransferConflict
	case err != nil:
		return OwnerTransfer{}, err
	}
	out := OwnerTransfer{
		FormerOwnerID:         caller.ID,
		FormerOwnerUsername:   caller.Username,
		NewOwnerID:            row.ID,
		NewOwnerUsername:      row.Username,
		PreviousOwnersCleared: row.PreviousOwnersCleared,
	}
	s.sendOwnershipNotices(ctx, caller, row)
	return out, nil
}

// sendOwnershipNotices mails both parties, best-effort and independently: a
// failure to reach one of them must neither roll back the transfer nor stop the
// other message. Neither body carries a credential or a token — they are
// after-the-fact notices, the same shape as SendPasswordChanged, and the one
// that reaches the former owner is the security notice that matters if the
// transfer was not their idea.
func (s *Service) sendOwnershipNotices(ctx context.Context, former sqlcgen.User, to sqlcgen.TransferInstanceOwnerRow) {
	if err := s.mailer.SendOwnershipTransferred(ctx, to.Email, to.Username, former.Username, s.consoleURL(), true); err != nil {
		s.logOwnershipNoticeFailure(ctx, "new_owner", err)
	}
	if err := s.mailer.SendOwnershipTransferred(ctx, former.Email, former.Username, to.Username, "", false); err != nil {
		s.logOwnershipNoticeFailure(ctx, "former_owner", err)
	}
}

// logOwnershipNoticeFailure records a failed transfer notice without naming
// either party's address — the observability denylist keeps addresses out of the
// log stream, and `party` says which side went unreached, which is what an
// operator needs.
func (s *Service) logOwnershipNoticeFailure(ctx context.Context, party string, err error) {
	slog.WarnContext(ctx, "ownership transfer notice failed", "party", party, "error", err)
}
