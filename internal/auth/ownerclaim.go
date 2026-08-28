package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// First-run owner bootstrap (0104). The first admin is created by redeeming a
// one-time claim token minted at boot and printed to the operator console —
// never by winning the registration race (the old "first registered account
// becomes admin" rule handed a fresh public install to whichever bot signed up
// first). While the claim is pending (empty users table + unclaimed token),
// every normal signup path answers ErrOwnerClaimRequired. Instances that
// already have users are implicitly claimed and never mint a NEW token — but
// an unclaimed leftover from an earlier boot still rotates (see
// EnsureOwnerClaimToken).

// ownerClaimTokenBytes is the entropy of a raw owner-claim token (256 bits) —
// the same construction as a refresh token: high-entropy random, so a fast
// hash (SHA-256) is the correct storage form.
const ownerClaimTokenBytes = 32

// generateOwnerClaimToken returns a new raw owner-claim token and its storage
// hash. The raw token is logged to the operator exactly once at mint time;
// only the hash is persisted, so a lost token is re-minted, never recovered.
func generateOwnerClaimToken() (raw, hash string, err error) {
	b := make([]byte, ownerClaimTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashOwnerClaimToken(raw), nil
}

// hashOwnerClaimToken returns the hex SHA-256 of a raw owner-claim token.
func hashOwnerClaimToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// WithFixedOwnerClaimToken pins every owner-claim mint to raw instead of a
// fresh random token (OWNER_CLAIM_TOKEN — dev/test-only, config refuses it in
// production), so harnesses and local dev claim the owner deterministically.
// Only the token's hash is still stored. Empty is ignored (random mint).
func WithFixedOwnerClaimToken(raw string) Option {
	return func(s *Service) { s.fixedOwnerClaimToken = raw }
}

// newOwnerClaimToken is the raw+hash pair a mint stores: the fixed override
// verbatim when configured (WithFixedOwnerClaimToken), else a fresh random.
func (s *Service) newOwnerClaimToken() (raw, hash string, err error) {
	if s.fixedOwnerClaimToken != "" {
		return s.fixedOwnerClaimToken, hashOwnerClaimToken(s.fixedOwnerClaimToken), nil
	}
	return generateOwnerClaimToken()
}

// EnsureOwnerClaimToken is the boot half of the bootstrap: it mints a fresh
// claim token (replacing — and thereby invalidating — any previous one) and
// returns the raw token for the caller to log once, whenever the instance
// still awaits its owner:
//
//   - users == 0: always mint, exactly as before (first run, or a restart
//     while unclaimed).
//   - users > 0 with an UNCLAIMED row: RE-MINT anyway — that token still
//     redeems for THE admin account, so it must rotate on every boot or a
//     leaked boot log stays a permanent admin credential. hadUsers reports
//     this case so the caller can escalate the log.
//   - users > 0 with no unclaimed row: strict no-op — the instance is
//     implicitly claimed (upgraded installs must never get a token).
func (s *Service) EnsureOwnerClaimToken(ctx context.Context) (token string, minted, hadUsers bool, err error) {
	n, err := s.repo.CountUsers(ctx)
	if err != nil {
		return "", false, false, err
	}
	if n > 0 {
		if _, err := s.repo.GetUnclaimedOwnerClaimToken(ctx); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", false, false, nil
			}
			return "", false, false, err
		}
	}
	raw, hash, err := s.newOwnerClaimToken()
	if err != nil {
		return "", false, false, err
	}
	if _, err := s.repo.UpsertOwnerClaimToken(ctx, hash); err != nil {
		return "", false, false, err
	}
	return raw, true, n > 0, nil
}

// OwnerClaimPending reports first-run state: an EMPTY users table AND an
// unclaimed claim token (so a database that never minted one, e.g. an
// in-memory test repo, reports false). It backs the public instance
// document's owner_claim_pending flag and the signup gate below.
func (s *Service) OwnerClaimPending(ctx context.Context) (bool, error) {
	n, err := s.repo.CountUsers(ctx)
	if err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil // implicitly claimed
	}
	if _, err := s.repo.GetUnclaimedOwnerClaimToken(ctx); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// refuseIfOwnerUnclaimed is the signup gate: every account-creating path calls
// it before touching the users table. It errors ErrOwnerClaimRequired while
// the instance awaits its owner (OwnerClaimPending). Repository errors are
// returned, never swallowed: failing open here is exactly the bug this flow
// replaces.
func (s *Service) refuseIfOwnerUnclaimed(ctx context.Context) error {
	pending, err := s.OwnerClaimPending(ctx)
	if err != nil {
		return err
	}
	if pending {
		return ErrOwnerClaimRequired
	}
	return nil
}

// ClaimOwnerInput is validated, normalized owner-claim data.
type ClaimOwnerInput struct {
	Token    string
	Username string
	Email    string
	Password string
}

// ClaimOwner redeems the one-time claim token for THE admin account and a
// fresh session. The token compare is constant-time in Go (never a SQL lookup
// keyed by the presented value), and the redeem + insert is a single atomic
// statement whose `claimed_at IS NULL` guard makes concurrent claims
// single-winner at the database — the loser gets ErrOwnerClaimInvalid, as does
// any wrong or already-redeemed token (one non-probing error for all cases).
// The W7 registration gates deliberately do not apply: the claimant IS the
// operator, so the account is neither held for approval nor for verification.
func (s *Service) ClaimOwner(ctx context.Context, in ClaimOwnerInput, userAgent string) (sqlcgen.User, Tokens, error) {
	row, err := s.repo.GetUnclaimedOwnerClaimToken(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlcgen.User{}, Tokens{}, ErrOwnerClaimInvalid
		}
		return sqlcgen.User{}, Tokens{}, err
	}
	if subtle.ConstantTimeCompare([]byte(hashOwnerClaimToken(in.Token)), []byte(row.TokenHash)) != 1 {
		return sqlcgen.User{}, Tokens{}, ErrOwnerClaimInvalid
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, err
	}
	created, err := s.repo.ClaimOwnerAndCreateAdmin(ctx, sqlcgen.ClaimOwnerAndCreateAdminParams{
		TokenHash:      row.TokenHash,
		Username:       strings.TrimSpace(in.Username),
		Email:          strings.TrimSpace(in.Email),
		PasswordHash:   hash,
		HistoryEnabled: s.newUserHistoryEnabled(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// A concurrent claim won the row between the fetch and the redeem.
			return sqlcgen.User{}, Tokens{}, ErrOwnerClaimInvalid
		}
		if pgconv.IsUniqueViolation(err) {
			return sqlcgen.User{}, Tokens{}, ErrConflict
		}
		return sqlcgen.User{}, Tokens{}, err
	}
	user := sqlcgen.User{
		ID: created.ID, Username: created.Username, Email: created.Email,
		PasswordHash: created.PasswordHash, Role: created.Role,
		EmailVerified: created.EmailVerified, IsActive: created.IsActive,
		CreatedAt: created.CreatedAt, UpdatedAt: created.UpdatedAt,
		DisplayName: created.DisplayName, Bio: created.Bio,
		PendingEmailVerification: created.PendingEmailVerification,
		HistoryEnabled:           created.HistoryEnabled,
	}
	tokens, err := s.issueTokens(ctx, user, userAgent)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, err
	}
	return user, tokens, nil
}
