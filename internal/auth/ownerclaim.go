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

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// First-run owner bootstrap (0104). The first admin is created by redeeming a
// one-time claim token minted at boot and printed to the operator console —
// never by winning the registration race (the old "first registered account
// becomes admin" rule handed a fresh public install to whichever bot signed up
// first). While the claim is pending (empty users table + unclaimed token),
// every normal signup path answers ErrOwnerClaimRequired. Instances that
// already have users never mint a token and are implicitly claimed.

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

// EnsureOwnerClaimToken is the boot half of the bootstrap: while the users
// table is EMPTY it mints a fresh claim token (replacing — and thereby
// invalidating — any previous one, claimed or not) and returns the raw token
// for the caller to log once. Once any user exists the instance is implicitly
// claimed and this is a no-op, so existing deployments are unaffected.
func (s *Service) EnsureOwnerClaimToken(ctx context.Context) (token string, minted bool, err error) {
	n, err := s.repo.CountUsers(ctx)
	if err != nil {
		return "", false, err
	}
	if n > 0 {
		return "", false, nil
	}
	raw, hash, err := generateOwnerClaimToken()
	if err != nil {
		return "", false, err
	}
	if _, err := s.repo.UpsertOwnerClaimToken(ctx, hash); err != nil {
		return "", false, err
	}
	return raw, true, nil
}

// refuseIfOwnerUnclaimed is the signup gate: every account-creating path calls
// it before touching the users table. It errors ErrOwnerClaimRequired while
// the instance awaits its owner (empty users table AND an unclaimed token —
// so a database that never minted one, e.g. an in-memory test repo, behaves
// as before). Repository errors are returned, never swallowed: failing open
// here is exactly the bug this flow replaces.
func (s *Service) refuseIfOwnerUnclaimed(ctx context.Context) error {
	n, err := s.repo.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil // implicitly claimed
	}
	if _, err := s.repo.GetUnclaimedOwnerClaimToken(ctx); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	return ErrOwnerClaimRequired
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
		if isUniqueViolation(err) {
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
