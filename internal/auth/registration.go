package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrRegistrationRequestNotFound means no PENDING registration request matches
// the id (unknown, or already approved/rejected).
var ErrRegistrationRequestNotFound = errors.New("auth: registration request not found")

// Registration request statuses.
const (
	RegistrationPending  = "pending"
	RegistrationApproved = "approved"
	RegistrationRejected = "rejected"
)

// RegistrationRequest is a queued signup awaiting admin approval, as shown in the
// admin queue. It never carries the password hash.
type RegistrationRequest struct {
	ID               uuid.UUID
	Username         string
	Email            string
	Note             string
	Status           string
	ModeratorNote    string
	ReviewedAt       *time.Time
	CreatedAt        time.Time
	ReviewerUsername string // "" until reviewed (or if that admin was deleted)
}

// RequestRegistration files a pending registration request (used when the
// instance requires approval). The password is bcrypt-hashed and stored on the
// request; the account is created later, on approval. A username/email already
// taken by a user or an existing pending request → ErrConflict.
func (s *Service) RequestRegistration(ctx context.Context, in RegisterInput, note string) (RegistrationRequest, error) {
	// While the instance awaits its owner there is nobody to approve a request
	// anyway — refuse like every other signup path (ownerclaim.go).
	if err := s.refuseIfOwnerUnclaimed(ctx); err != nil {
		return RegistrationRequest{}, err
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return RegistrationRequest{}, err
	}
	// Reject up front if the email already belongs to an account (the pending
	// unique indexes cover duplicate pending requests; a username already taken
	// by a user is caught atomically at approval time).
	if _, err := s.repo.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(in.Email))); err == nil {
		return RegistrationRequest{}, ErrConflict
	}
	row, err := s.repo.CreateRegistrationRequest(ctx, sqlcgen.CreateRegistrationRequestParams{
		Username:     strings.TrimSpace(in.Username),
		Email:        strings.TrimSpace(in.Email),
		PasswordHash: hash,
		Note:         strings.TrimSpace(note),
	})
	if err != nil {
		if pgconv.IsUniqueViolation(err) {
			return RegistrationRequest{}, ErrConflict
		}
		return RegistrationRequest{}, err
	}
	return RegistrationRequest{
		ID: row.ID, Username: row.Username, Email: row.Email, Note: row.Note,
		Status: row.Status, ModeratorNote: row.ModeratorNote,
		ReviewedAt: pgconv.TimeOrNil(row.ReviewedAt), CreatedAt: row.CreatedAt,
	}, nil
}

// ListRegistrationRequests returns the approval queue, newest first, together
// with how many requests match the same status filter. status is the exact
// lifecycle state to show, or "" for all of them — it used to be a pendingOnly
// bool, which made ?status=approved indistinguishable from no filter. The
// caller clamps limit/offset.
func (s *Service) ListRegistrationRequests(ctx context.Context, status string, limit, offset int32) ([]RegistrationRequest, int64, error) {
	filter := nilIfEmptyString(status)
	rows, err := s.repo.ListRegistrationRequests(ctx, sqlcgen.ListRegistrationRequestsParams{
		Status:       filter,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountRegistrationRequests(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	out := make([]RegistrationRequest, 0, len(rows))
	for _, r := range rows {
		reviewer := ""
		if r.ReviewerUsername != nil {
			reviewer = *r.ReviewerUsername
		}
		out = append(out, RegistrationRequest{
			ID: r.ID, Username: r.Username, Email: r.Email, Note: r.Note,
			Status: r.Status, ModeratorNote: r.ModeratorNote,
			ReviewedAt: pgconv.TimeOrNil(r.ReviewedAt), CreatedAt: r.CreatedAt, ReviewerUsername: reviewer,
		})
	}
	return out, total, nil
}

// nilIfEmptyString maps an empty filter string to a NULL query parameter ("no
// filter"), so "" returns every row rather than rows with an empty status.
func nilIfEmptyString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// ApproveRegistration approves a pending request, creating the account from the
// stored hash and marking the request approved atomically. An unknown/already
// resolved id → ErrRegistrationRequestNotFound; a username/email now taken →
// ErrConflict. Returns the created user.
//
// COMPOSITION with the email-verification gate (config-parity W7): approval
// runs first (the codebase's natural order — no account exists until the admin
// approves), then, when the gate is effective at approval time, the created
// account is additionally held pending email verification and the verification
// message is sent. A send failure never fails the approval (same posture as
// RegisterPendingVerification): the account exists held, and the operator
// recovery path is the admin email_verified override.
func (s *Service) ApproveRegistration(ctx context.Context, adminID, requestID uuid.UUID) (sqlcgen.User, error) {
	gateActive := s.EmailVerificationGateActive()
	row, err := s.repo.ApproveRegistrationRequest(ctx, sqlcgen.ApproveRegistrationRequestParams{
		ID:                       requestID,
		ReviewedBy:               pgconv.UUID(adminID),
		PendingEmailVerification: gateActive,
		HistoryEnabled:           s.newUserHistoryEnabled(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlcgen.User{}, ErrRegistrationRequestNotFound
		}
		if pgconv.IsUniqueViolation(err) {
			return sqlcgen.User{}, ErrConflict
		}
		return sqlcgen.User{}, err
	}
	user := sqlcgen.User{
		ID: row.ID, Username: row.Username, Email: row.Email, PasswordHash: row.PasswordHash,
		Role: row.Role, EmailVerified: row.EmailVerified, IsActive: row.IsActive,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, DisplayName: row.DisplayName, Bio: row.Bio,
		PendingEmailVerification: row.PendingEmailVerification,
	}
	if gateActive {
		// Best-effort verification send (see the composition note above).
		_ = s.RequestEmailVerification(ctx, user.ID)
	}
	// Tell the applicant. Best-effort by the same rule the rest of this package
	// follows: the account exists either way, and a relay that is down must not
	// strand a request in the queue with the reviewer believing they resolved it.
	if err := s.mailer.SendRegistrationApproved(ctx, user.Email, user.Username, s.signInURL(), gateActive); err != nil {
		// The mail package's error text never carries the address.
		s.logRegistrationNoticeFailure(ctx, "approved", err)
	}
	return user, nil
}

// logRegistrationNoticeFailure records a failed signup-decision notice without
// naming the applicant: the address is PII and the observability denylist keeps
// it out of the log stream.
func (s *Service) logRegistrationNoticeFailure(ctx context.Context, decision string, err error) {
	slog.WarnContext(ctx, "registration decision notice failed", "decision", decision, "error", err)
}

// RejectRegistration rejects a pending request with a moderator note. An
// unknown/already resolved id → ErrRegistrationRequestNotFound.
//
// The applicant is then told, and the note goes with them: it was written for
// exactly one reader, and until A16 it reached nobody — a rejected applicant
// discovered the outcome only by failing to sign in, which is also what a
// request still sitting in the queue looks like. The send is best-effort: the
// rejection is already recorded, and re-running it is a 404.
func (s *Service) RejectRegistration(ctx context.Context, adminID, requestID uuid.UUID, note string) error {
	note = strings.TrimSpace(note)
	row, err := s.repo.RejectRegistrationRequest(ctx, sqlcgen.RejectRegistrationRequestParams{
		ID:            requestID,
		ModeratorNote: note,
		ReviewedBy:    pgconv.UUID(adminID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRegistrationRequestNotFound
		}
		return err
	}
	if err := s.mailer.SendRegistrationRejected(ctx, row.Email, row.Username, note); err != nil {
		s.logRegistrationNoticeFailure(ctx, "rejected", err)
	}
	return nil
}
