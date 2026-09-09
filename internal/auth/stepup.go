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

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Step-up authentication for accounts whose only credential is an external
// provider.
//
// An account created by ATProto or OIDC login is passwordless and carries a
// deliberately unroutable placeholder address (…@atproto.invalid, RFC 2606).
// Every sensitive self-service action in this service rests on one of two
// proofs — "supply your current password" or "follow the link we mailed you" —
// and that account can satisfy neither. A30 measured the consequence: the
// unlink refusal that protects the account's last sign-in method tells the user
// to "set a password first", and no path allowed it. Lose the Bluesky account
// and the Vidra account went with it.
//
// The third proof is the one the account already has: a fresh, completed OAuth
// round trip through the provider that IS its credential. A step-up token is
// the server-side record that such a round trip just happened —
//
//	minted   by the login CALLBACK when it carries a step_up purpose, only
//	         after the same iss / sub / scope invariants a login enforces, and
//	         only when the verified subject is an identity ALREADY linked to
//	         the account holding the session;
//	bound    to (user, session): the assertion authorises the browser that made
//	         it and nothing else, so a token lifted out of one session's
//	         redirect is worthless in another;
//	single-use and short-lived: consumed by the same statement that reads it,
//	         and dead after stepUpTTL.
//
// It is deliberately NOT a JWT or a signed cookie. Statelessness and single-use
// are incompatible: a stateless token can be replayed until it expires, and
// "this assertion has already been spent" is exactly the property that makes it
// safe to hand to the browser in a redirect.

// stepUpTTL bounds how long a completed provider round trip stays spendable.
// Ten minutes: long enough to survive the redirect back plus a user typing a
// password into the form it unlocks, short enough that an assertion is never a
// standing authorisation to change the account's credentials.
const stepUpTTL = 10 * time.Minute

// stepUpTokenBytes is the entropy of a raw step-up token (256 bits), matching
// the refresh, reset, verification and owner-claim tokens.
const stepUpTokenBytes = 32

// Sentinel errors for the step-up flow.
var (
	// ErrStepUpRequired means the request carried no usable step-up assertion:
	// none was supplied, or the one supplied is unknown, already spent,
	// expired, issued to another account, or issued to another session.
	// Deliberately ONE error for all of those so a caller cannot probe which —
	// the same discipline the reset and email-change tokens follow.
	ErrStepUpRequired = errors.New("auth: a fresh provider sign-in is required")
	// ErrPasswordAlreadySet means a set-password (or password-less email
	// change) was attempted on an account that HAS a password. It is not a
	// failure of authorisation but a routing answer: the change flow, which
	// re-verifies the current password, is the correct door.
	ErrPasswordAlreadySet = errors.New("auth: account already has a password")
	// ErrNoLinkedIdentity means the account has no identity for the provider a
	// step-up was attempted with — so a completed round trip with it proves
	// nothing about this account.
	ErrNoLinkedIdentity = errors.New("auth: no identity linked for that provider")
)

// generateStepUpToken returns a high-entropy opaque token and its storage hash.
// The raw token is handed to the browser exactly once, in the callback's
// redirect; only the hash is persisted. SHA-256 is correct for an already-random
// token — bcrypt is only for low-entropy passwords.
func generateStepUpToken() (raw, hash string, err error) {
	b := make([]byte, stepUpTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashStepUpToken(raw), nil
}

// hashStepUpToken returns the hex SHA-256 of a raw token, the lookup key in
// step_up_tokens.
func hashStepUpToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// StepUpGrant is what a completed challenge hands back: the raw token (the only
// time it exists outside the browser) and when it stops working, so the UI can
// say how long the user has.
type StepUpGrant struct {
	Token     string
	Provider  string
	ExpiresAt time.Time
}

// IssueStepUp records a completed provider re-authentication for one session
// and returns the single-use token that spends it.
//
// It supersedes the session's previous unspent token first — at most one
// step-up is ever live per session, so a user who starts the challenge twice
// cannot leave a spare assertion lying in an old browser tab — and sweeps
// expired rows on the way past, which keeps the table bounded without a cron.
func (s *Service) IssueStepUp(ctx context.Context, userID, sessionID uuid.UUID, provider string) (StepUpGrant, error) {
	raw, hash, err := generateStepUpToken()
	if err != nil {
		return StepUpGrant{}, err
	}
	// Supersede first: the old assertion must be dead before the new one
	// exists, never the other way round.
	if _, err := s.repo.DeleteUnusedStepUpTokensForSession(ctx, sessionID); err != nil {
		return StepUpGrant{}, err
	}
	// Best-effort housekeeping. A failure here loses nothing but disk.
	_, _ = s.repo.DeleteExpiredStepUpTokens(ctx)
	expires := s.now().Add(stepUpTTL)
	if _, err := s.repo.CreateStepUpToken(ctx, sqlcgen.CreateStepUpTokenParams{
		UserID:    userID,
		SessionID: sessionID,
		Provider:  provider,
		TokenHash: hash,
		ExpiresAt: expires,
	}); err != nil {
		return StepUpGrant{}, err
	}
	return StepUpGrant{Token: raw, Provider: provider, ExpiresAt: expires}, nil
}

// ConsumeStepUp spends a step-up assertion for userID on the session the caller
// is actually using, returning the provider that satisfied it. Every invalid
// case — no token, malformed session, unknown/spent/expired token, another
// account's token, another session's token — is ErrStepUpRequired.
func (s *Service) ConsumeStepUp(ctx context.Context, userID uuid.UUID, currentSessionID, rawToken string) (string, error) {
	if strings.TrimSpace(rawToken) == "" {
		return "", ErrStepUpRequired
	}
	sessionID, err := uuid.Parse(currentSessionID)
	if err != nil {
		// A caller outside the normal session-bound path cannot present a
		// binding, so it cannot spend an assertion either.
		return "", ErrStepUpRequired
	}
	row, err := s.repo.ConsumeStepUpToken(ctx, sqlcgen.ConsumeStepUpTokenParams{
		TokenHash: hashStepUpToken(rawToken),
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		return "", ErrStepUpRequired
	}
	return row.Provider, nil
}

// StepUpProvidersFor lists the providers that can satisfy a step-up for an
// account: exactly the identities it has linked. It is what the 403 names, so
// the client can offer the right button instead of a generic refusal — and it
// is empty for a password account, which needs no step-up at all.
func (s *Service) StepUpProvidersFor(ctx context.Context, userID uuid.UUID) []string {
	idents, err := s.repo.ListOAuthIdentitiesByUser(ctx, userID)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(idents))
	for _, id := range idents {
		out = append(out, id.Provider)
	}
	return out
}

// HasLinkedProvider reports whether the account has an identity for provider —
// the check the step-up START performs, so a challenge is never begun against a
// provider whose completion could not authorise anything.
func (s *Service) HasLinkedProvider(ctx context.Context, userID uuid.UUID, provider string) bool {
	for _, p := range s.StepUpProvidersFor(ctx, userID) {
		if p == provider {
			return true
		}
	}
	return false
}

// unroutableEmailSuffix is the RFC 2606 reserved TLD every synthetic
// provider-account address sits under. A name in it can never resolve, so mail
// to it can never be delivered — that is why it was chosen for accounts with no
// real address, and why any promise to "mail you a notice" at one is a lie.
const unroutableEmailSuffix = ".invalid"

// IsPlaceholderEmail reports whether addr is a synthetic, never-deliverable
// address — the shape an ATProto-created account is given
// (did-plc-…@atproto.invalid). Two things depend on it: the account's own
// settings page, which has to tell the user their account is unrecoverable, and
// the email-change confirmation, which must not promise a notice to a mailbox
// that cannot exist.
func IsPlaceholderEmail(addr string) bool {
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return false
	}
	domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(addr[at+1:]), "."))
	return strings.HasSuffix(domain, unroutableEmailSuffix)
}
