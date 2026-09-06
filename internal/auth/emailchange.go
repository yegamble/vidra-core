package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sentinel errors for the two-step email change. Like the reset and
// verification tokens, an invalid token is ONE error for every invalid case
// (unknown, used, expired, or belonging to another account) so a caller cannot
// probe which.
var (
	// ErrEmailUnchanged means the requested address is the one the account
	// already has. Refused rather than performed: a "change" to the same
	// address would mail a confirmation and a change notice for nothing.
	ErrEmailUnchanged = errors.New("auth: email unchanged")
	// ErrEmailTaken means the requested address already resolves to another
	// account's sign-in identifier — either its email or, because sign-in
	// accepts "email OR username" with EMAIL TAKING PRECEDENCE, its username.
	// A username-shaped lookalike must be refused too: usernames predating the
	// '@' ban may literally equal an address, and taking it would silently
	// shadow that account's sign-in.
	ErrEmailTaken = errors.New("auth: email already in use")
	// ErrInvalidEmailChangeToken means the confirmation token is unknown,
	// already used, expired, or was issued to a different account.
	ErrInvalidEmailChangeToken = errors.New("auth: invalid or expired email change token")
	// ErrNoPendingEmailChange means there is no live pending request to resend
	// or cancel.
	ErrNoPendingEmailChange = errors.New("auth: no pending email change")
)

// emailChangeTTL is how long a pending email change stays confirmable. It
// matches the password-reset window rather than the 24h email-verification one:
// the user is in their settings when they ask, so an hour is ample, and the
// pending request is a standing instruction to move the address — the shorter
// it can be, the smaller the window in which a stolen mailbox link is worth
// anything.
const emailChangeTTL = time.Hour

// emailChangeTokenBytes is the entropy of a raw email-change token (256 bits),
// matching the reset and verification tokens.
const emailChangeTokenBytes = 32

// generateEmailChangeToken returns a high-entropy opaque token and its storage
// hash. The raw token is delivered once, to the NEW address; only the hash is
// persisted. SHA-256 is correct for an already-random token — bcrypt is only
// for low-entropy passwords.
func generateEmailChangeToken() (raw, hash string, err error) {
	b := make([]byte, emailChangeTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashEmailChangeToken(raw), nil
}

// hashEmailChangeToken returns the hex SHA-256 of a raw token, the lookup key in
// email_change_requests.
func hashEmailChangeToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// PendingEmailChange is the readable pending state: which address is awaiting
// confirmation, when it was asked for, and when the link stops working. The
// token never appears here — it exists only in the message.
type PendingEmailChange struct {
	NewEmail    string
	RequestedAt time.Time
	ExpiresAt   time.Time
}

// RequestEmailChange starts the two-step change: it re-verifies the CURRENT
// password, records a pending request for newEmail, and mails a single-use,
// expiring token to that NEW address. The account's live address is NOT touched
// — possession of the new mailbox is still unproven — so a stolen access token
// alone can move nothing, and a typo costs the user a cancel rather than their
// account.
//
// A second request supersedes the first: the previous unused request is deleted
// before the new one is written, so exactly one token is ever live.
//
// The refusals are deliberate. A password-less account (OAuth/ATProto-only) gets
// ErrPasswordNotSet, not "incorrect password", for the same reason the password
// change does: bcrypt can never verify an empty hash, so no supplied password
// could satisfy it. An address that already resolves to another account's
// sign-in identifier gets ErrEmailTaken — the same thing registration discloses
// today with its 409.
func (s *Service) RequestEmailChange(ctx context.Context, userID uuid.UUID, currentPassword, newEmail string) (PendingEmailChange, error) {
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return PendingEmailChange{}, err
	}
	if user.PasswordHash == "" {
		return PendingEmailChange{}, ErrPasswordNotSet
	}
	if err := CheckPassword(user.PasswordHash, currentPassword); err != nil {
		return PendingEmailChange{}, ErrInvalidPassword
	}
	addr := strings.TrimSpace(newEmail)
	if strings.EqualFold(addr, user.Email) {
		return PendingEmailChange{}, ErrEmailUnchanged
	}
	if err := s.emailAvailableFor(ctx, user.ID, addr); err != nil {
		return PendingEmailChange{}, err
	}
	raw, hash, err := generateEmailChangeToken()
	if err != nil {
		return PendingEmailChange{}, err
	}
	// Supersede first: the old token must be dead before the new one exists,
	// never the other way round.
	if _, err := s.repo.DeleteUnusedEmailChangeRequests(ctx, user.ID); err != nil {
		return PendingEmailChange{}, err
	}
	row, err := s.repo.CreateEmailChangeRequest(ctx, sqlcgen.CreateEmailChangeRequestParams{
		UserID:    user.ID,
		NewEmail:  addr,
		TokenHash: hash,
		ExpiresAt: s.now().Add(emailChangeTTL),
	})
	if err != nil {
		return PendingEmailChange{}, err
	}
	pending := PendingEmailChange{NewEmail: row.NewEmail, RequestedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt}
	// The confirmation goes to the NEW address and nowhere else: it is the
	// possession proof, and sending it to the old one would prove nothing.
	// Unlike the change notice this is NOT best-effort — a pending request whose
	// message never left is a dead end the user cannot see.
	if err := s.mailer.SendEmailChangeVerification(ctx, addr, raw); err != nil {
		return PendingEmailChange{}, err
	}
	return pending, nil
}

// ResendEmailChange re-issues the confirmation for the address already pending,
// superseding the previous token. It deliberately does NOT re-ask for the
// password: the pending row is itself the record that the password was proven,
// and the message can only ever go to the address that request already named,
// so a stolen access token gains nothing it did not already have. The route is
// rate-limited like the rest of the auth group.
func (s *Service) ResendEmailChange(ctx context.Context, userID uuid.UUID) (PendingEmailChange, error) {
	current, err := s.repo.GetPendingEmailChangeRequest(ctx, userID)
	if err != nil {
		return PendingEmailChange{}, ErrNoPendingEmailChange
	}
	raw, hash, err := generateEmailChangeToken()
	if err != nil {
		return PendingEmailChange{}, err
	}
	if _, err := s.repo.DeleteUnusedEmailChangeRequests(ctx, userID); err != nil {
		return PendingEmailChange{}, err
	}
	row, err := s.repo.CreateEmailChangeRequest(ctx, sqlcgen.CreateEmailChangeRequestParams{
		UserID:    userID,
		NewEmail:  current.NewEmail,
		TokenHash: hash,
		ExpiresAt: s.now().Add(emailChangeTTL),
	})
	if err != nil {
		return PendingEmailChange{}, err
	}
	pending := PendingEmailChange{NewEmail: row.NewEmail, RequestedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt}
	if err := s.mailer.SendEmailChangeVerification(ctx, row.NewEmail, raw); err != nil {
		return PendingEmailChange{}, err
	}
	return pending, nil
}

// PendingEmailChangeFor returns the account's live pending request, or
// ErrNoPendingEmailChange when there is none (including when the last one
// expired — an expired request is not pending).
func (s *Service) PendingEmailChangeFor(ctx context.Context, userID uuid.UUID) (PendingEmailChange, error) {
	row, err := s.repo.GetPendingEmailChangeRequest(ctx, userID)
	if err != nil {
		return PendingEmailChange{}, ErrNoPendingEmailChange
	}
	return PendingEmailChange{NewEmail: row.NewEmail, RequestedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt}, nil
}

// CancelEmailChange drops the pending request, killing its token. It reports
// ErrNoPendingEmailChange when there was nothing to cancel rather than claiming
// success for a no-op.
func (s *Service) CancelEmailChange(ctx context.Context, userID uuid.UUID) error {
	n, err := s.repo.DeleteUnusedEmailChangeRequests(ctx, userID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNoPendingEmailChange
	}
	return nil
}

// ConfirmEmailChange consumes the token delivered to the new address and moves
// the account onto it. The switch is ONE statement (ConfirmEmailChange in
// email_changes.sql): the token is consumed and the address moved together, so
// there is no window in which the token is spent and the address is not, and two
// concurrent confirmations cannot both succeed. userID scopes the lookup, so
// another account's token is refused exactly like an unknown one.
//
// It returns the account's OLD address alongside the new one: the caller mails
// the old mailbox a notice, which is the only signal that reaches a user whose
// address was taken from them.
func (s *Service) ConfirmEmailChange(ctx context.Context, userID uuid.UUID, rawToken string) (oldEmail, newEmail string, err error) {
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return "", "", err
	}
	row, err := s.repo.ConfirmEmailChange(ctx, sqlcgen.ConfirmEmailChangeParams{
		TokenHash: hashEmailChangeToken(rawToken),
		UserID:    userID,
	})
	if err != nil {
		// The address may have been claimed by somebody else between the
		// request and the confirmation; users_email_lower_idx is the authority
		// and reports it as a unique violation, which is a 409, not a dead
		// token. Anything else is indistinct by design.
		if pgconv.IsUniqueViolation(err) {
			return "", "", ErrEmailTaken
		}
		return "", "", ErrInvalidEmailChangeToken
	}
	// Best-effort from here: the address has already moved, and a failure to
	// tidy up or to mail must not be reported as "your address did not change".
	_, _ = s.repo.DeleteUnusedEmailChangeRequests(ctx, userID)
	_ = s.mailer.SendEmailChanged(ctx, user.Email, row.Email)
	return user.Email, row.Email, nil
}

// emailAvailableFor reports whether addr may become userID's address. It uses
// the SIGN-IN lookup rather than a plain email lookup, so it refuses an address
// that matches another account's email OR its username: sign-in resolves both
// with email winning, so an address equal to somebody's username would shadow
// their sign-in the moment it became an email.
func (s *Service) emailAvailableFor(ctx context.Context, userID uuid.UUID, addr string) error {
	other, err := s.repo.GetUserByLoginIdentifier(ctx, addr)
	if err == nil && other.ID != userID {
		return ErrEmailTaken
	}
	return nil
}
