package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TOTP two-factor authentication (fix_plan P4). RFC 6238 SHA1 / 6 digits /
// 30s period with ±1 step of clock skew, generated and validated through
// github.com/pquerna/otp — the de-facto standard Go TOTP implementation
// (Apache-2.0, maintained, tiny; RFC 6238 test vectors included upstream).
// Rolling our own HMAC-window arithmetic here would be the riskier choice.
//
// The shared secret is envelope-encrypted at rest via internal/secretbox under
// MFA_KEY_KEK (falling back to FEDERATION_KEY_KEK); with no KEK (dev) it is
// stored raw and cmd/api warns loudly. The secret and the otpauth:// URI leave
// the server exactly once, in the enrollment response — never in logs.

// MFA sentinel errors the HTTP layer maps to status codes.
var (
	// ErrMFAUnavailable means the MFA collaborators were not wired (WithMFA).
	ErrMFAUnavailable = errors.New("auth: mfa is not configured on this server")
	// ErrMFAAlreadyEnabled means TOTP is already enabled for the account.
	ErrMFAAlreadyEnabled = errors.New("auth: mfa is already enabled")
	// ErrMFANotEnrolled means there is no pending TOTP enrollment to confirm.
	ErrMFANotEnrolled = errors.New("auth: no totp enrollment in progress")
	// ErrMFANotEnabled means TOTP is not enabled for the account.
	ErrMFANotEnabled = errors.New("auth: mfa is not enabled")
	// ErrInvalidMFACode means the presented TOTP or recovery code did not
	// verify (wrong, expired, or already-used recovery code).
	ErrInvalidMFACode = errors.New("auth: invalid mfa code")
	// ErrInvalidMFAToken means the mfa_token is missing, tampered, expired, or
	// no longer matches an MFA-enabled active account.
	ErrInvalidMFAToken = errors.New("auth: invalid or expired mfa token")
)

// MFARepository is the data access the TOTP flows need. *sqlcgen.Queries
// satisfies it directly.
type MFARepository interface {
	UpsertUserMFA(ctx context.Context, arg sqlcgen.UpsertUserMFAParams) (sqlcgen.UserMfa, error)
	GetUserMFA(ctx context.Context, userID uuid.UUID) (sqlcgen.UserMfa, error)
	EnableUserMFA(ctx context.Context, userID uuid.UUID) (int64, error)
	DeleteUserMFA(ctx context.Context, userID uuid.UUID) (int64, error)
	CreateRecoveryCode(ctx context.Context, arg sqlcgen.CreateRecoveryCodeParams) error
	DeleteRecoveryCodes(ctx context.Context, userID uuid.UUID) error
	UseRecoveryCode(ctx context.Context, arg sqlcgen.UseRecoveryCodeParams) (int64, error)
	CountUnusedRecoveryCodes(ctx context.Context, userID uuid.UUID) (int64, error)
}

// mfaTokenTTL is the lifetime of the single-purpose mfa_token issued by a
// credential-valid login on an MFA-enabled account.
const mfaTokenTTL = 5 * time.Minute

// mfaAudienceSuffix scopes the mfa_token to the challenge endpoint: an
// mfa_token never parses as an access token (audience mismatch in the auth
// middleware) and an access token never parses as an mfa_token.
const mfaAudienceSuffix = ":mfa"

// recoveryCodeCount is how many single-use recovery codes an enable issues.
const recoveryCodeCount = 10

// totpValidateOpts pins the RFC 6238 profile: SHA1, 6 digits, 30s period, and
// ±1 step of skew so a code from the adjacent window still verifies.
var totpValidateOpts = totp.ValidateOpts{
	Period:    30,
	Skew:      1,
	Digits:    otp.DigitsSix,
	Algorithm: otp.AlgorithmSHA1,
}

// WithMFA wires the TOTP collaborators: the repository, the at-rest secret
// cipher (nil in dev stores secrets raw), and the issuer label shown by
// authenticator apps (TOTP_ISSUER, default instance name). Without this
// option the MFA endpoints answer ErrMFAUnavailable and login is unchanged.
func WithMFA(repo MFARepository, cipher *secretbox.Cipher, issuerName string) Option {
	return func(s *Service) {
		if repo == nil {
			return
		}
		s.mfaRepo = repo
		s.mfaCipher = cipher
		s.mfaIssuerName = issuerName
		s.mfaTokens = s.issuer.forMFA()
	}
}

// forMFA derives a TokenIssuer for the single-purpose mfa_token: same secret
// and issuer, audience suffixed with ":mfa", 5-minute lifetime.
func (t *TokenIssuer) forMFA() *TokenIssuer {
	return &TokenIssuer{
		secret:   t.secret,
		issuer:   t.issuer,
		audience: t.audience + mfaAudienceSuffix,
		ttl:      mfaTokenTTL,
		now:      t.now,
	}
}

// TOTPEnrollment is returned exactly once when an enrollment starts. Neither
// field is ever persisted in plaintext or logged.
type TOTPEnrollment struct {
	// Secret is the base32 shared secret for manual authenticator entry.
	Secret string
	// URI is the otpauth:// provisioning URI (QR-code payload).
	URI string
}

// BeginTOTPEnrollment generates a fresh TOTP secret for the account and stores
// it sealed with enabled=false (pending). Restarting a pending enrollment
// replaces the secret; an already-enabled account gets ErrMFAAlreadyEnabled
// (disable first). Login is unaffected until the enrollment is verified.
func (s *Service) BeginTOTPEnrollment(ctx context.Context, userID uuid.UUID) (TOTPEnrollment, error) {
	if s.mfaRepo == nil {
		return TOTPEnrollment{}, ErrMFAUnavailable
	}
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	if row, err := s.mfaRepo.GetUserMFA(ctx, userID); err == nil && row.Enabled {
		return TOTPEnrollment{}, ErrMFAAlreadyEnabled
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      s.mfaIssuerName,
		AccountName: user.Email,
	})
	if err != nil {
		return TOTPEnrollment{}, err
	}
	stored, err := s.sealTOTPSecret(key.Secret())
	if err != nil {
		return TOTPEnrollment{}, err
	}
	if _, err := s.mfaRepo.UpsertUserMFA(ctx, sqlcgen.UpsertUserMFAParams{
		UserID:           userID,
		TotpSecretSealed: stored,
	}); err != nil {
		// The upsert's WHERE NOT enabled guard makes a concurrent enable surface
		// here as no-row; report it as the state conflict it is.
		return TOTPEnrollment{}, ErrMFAAlreadyEnabled
	}
	return TOTPEnrollment{Secret: key.Secret(), URI: key.URL()}, nil
}

// VerifyTOTPEnrollment confirms a pending enrollment: the first valid code
// flips enabled=true and issues the recovery codes, returned in plaintext
// exactly once. A wrong code is ErrInvalidMFACode and the enrollment stays
// pending.
func (s *Service) VerifyTOTPEnrollment(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	if s.mfaRepo == nil {
		return nil, ErrMFAUnavailable
	}
	row, err := s.mfaRepo.GetUserMFA(ctx, userID)
	if err != nil {
		return nil, ErrMFANotEnrolled
	}
	if row.Enabled {
		return nil, ErrMFAAlreadyEnabled
	}
	secret, err := s.openTOTPSecret(row.TotpSecretSealed)
	if err != nil {
		return nil, err
	}
	if !s.validTOTPCode(code, secret) {
		return nil, ErrInvalidMFACode
	}
	n, err := s.mfaRepo.EnableUserMFA(ctx, userID)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		// Lost a race with another verify/disable; nothing was enabled by us.
		return nil, ErrMFANotEnrolled
	}
	return s.issueRecoveryCodes(ctx, userID)
}

// issueRecoveryCodes replaces the user's recovery codes with a fresh set and
// returns the raw codes (shown once). Only SHA-256 hashes are stored.
func (s *Service) issueRecoveryCodes(ctx context.Context, userID uuid.UUID) ([]string, error) {
	if err := s.mfaRepo.DeleteRecoveryCodes(ctx, userID); err != nil {
		return nil, err
	}
	codes := make([]string, 0, recoveryCodeCount)
	for len(codes) < recoveryCodeCount {
		code, err := generateRecoveryCode()
		if err != nil {
			return nil, err
		}
		if err := s.mfaRepo.CreateRecoveryCode(ctx, sqlcgen.CreateRecoveryCodeParams{
			UserID:   userID,
			CodeHash: hashRecoveryCode(code),
		}); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}

// DisableTOTP turns MFA off after re-confirming the account password (a stolen
// access token alone must not be able to strip 2FA). All recovery codes are
// dropped. ErrInvalidPassword on a wrong password; ErrMFANotEnabled when there
// is nothing (pending or enabled) to disable.
func (s *Service) DisableTOTP(ctx context.Context, userID uuid.UUID, password string) error {
	if s.mfaRepo == nil {
		return ErrMFAUnavailable
	}
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := CheckPassword(user.PasswordHash, password); err != nil {
		return ErrInvalidPassword
	}
	n, err := s.mfaRepo.DeleteUserMFA(ctx, userID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrMFANotEnabled
	}
	return s.mfaRepo.DeleteRecoveryCodes(ctx, userID)
}

// MFAStatus is the account's two-factor state.
type MFAStatus struct {
	Enabled bool
	// RecoveryCodesRemaining counts unused recovery codes (0 when disabled).
	RecoveryCodesRemaining int64
}

// GetMFAStatus reports whether TOTP is enabled and how many recovery codes
// remain. A pending (unverified) enrollment reports as disabled.
func (s *Service) GetMFAStatus(ctx context.Context, userID uuid.UUID) (MFAStatus, error) {
	if s.mfaRepo == nil {
		return MFAStatus{}, ErrMFAUnavailable
	}
	row, err := s.mfaRepo.GetUserMFA(ctx, userID)
	if err != nil || !row.Enabled {
		return MFAStatus{}, nil
	}
	remaining, err := s.mfaRepo.CountUnusedRecoveryCodes(ctx, userID)
	if err != nil {
		return MFAStatus{}, err
	}
	return MFAStatus{Enabled: true, RecoveryCodesRemaining: remaining}, nil
}

// MFAChallengeMethod classifies how a challenge was satisfied (for audit
// events — never the code itself).
type MFAChallengeMethod string

const (
	MFAMethodTOTP     MFAChallengeMethod = "totp"
	MFAMethodRecovery MFAChallengeMethod = "recovery_code"
)

// CompleteMFAChallenge finishes an MFA login: it validates the single-purpose
// mfa_token, then the TOTP code (±1 step skew) or a recovery code (marked used
// — single-use), and only then issues the session. Token problems are
// ErrInvalidMFAToken; code problems are ErrInvalidMFACode.
func (s *Service) CompleteMFAChallenge(ctx context.Context, mfaToken, code, userAgent string) (sqlcgen.User, Tokens, MFAChallengeMethod, error) {
	if s.mfaRepo == nil {
		return sqlcgen.User{}, Tokens{}, "", ErrMFAUnavailable
	}
	claims, err := s.mfaTokens.Parse(mfaToken)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, "", ErrInvalidMFAToken
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, "", ErrInvalidMFAToken
	}
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, "", ErrInvalidMFAToken
	}
	row, err := s.mfaRepo.GetUserMFA(ctx, userID)
	if err != nil || !row.Enabled {
		// MFA was disabled between login and challenge — the token's purpose is
		// gone; the client should log in again (which now succeeds directly).
		return sqlcgen.User{}, Tokens{}, "", ErrInvalidMFAToken
	}

	method := MFAMethodTOTP
	if looksLikeTOTPCode(code) {
		secret, err := s.openTOTPSecret(row.TotpSecretSealed)
		if err != nil {
			return sqlcgen.User{}, Tokens{}, "", err
		}
		if !s.validTOTPCode(code, secret) {
			return sqlcgen.User{}, Tokens{}, "", ErrInvalidMFACode
		}
	} else {
		// Recovery code: redeem exactly one unused matching row (single-use).
		n, err := s.mfaRepo.UseRecoveryCode(ctx, sqlcgen.UseRecoveryCodeParams{
			UserID:   userID,
			CodeHash: hashRecoveryCode(code),
		})
		if err != nil {
			return sqlcgen.User{}, Tokens{}, "", err
		}
		if n == 0 {
			return sqlcgen.User{}, Tokens{}, "", ErrInvalidMFACode
		}
		method = MFAMethodRecovery
	}

	tokens, err := s.issueTokens(ctx, user, userAgent)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, "", err
	}
	return user, tokens, method, nil
}

// mfaEnabled reports whether the account has verified TOTP on. False whenever
// MFA is not wired, unknown, or still pending.
func (s *Service) mfaEnabled(ctx context.Context, userID uuid.UUID) bool {
	if s.mfaRepo == nil {
		return false
	}
	row, err := s.mfaRepo.GetUserMFA(ctx, userID)
	return err == nil && row.Enabled
}

// validTOTPCode checks an RFC 6238 code against the base32 secret at the
// service clock, allowing ±1 period of skew.
func (s *Service) validTOTPCode(code, secret string) bool {
	ok, err := totp.ValidateCustom(strings.TrimSpace(code), secret, s.now(), totpValidateOpts)
	return err == nil && ok
}

// looksLikeTOTPCode distinguishes a 6-digit TOTP code from a recovery code
// (10 hex chars + dash), so the challenge knows which verifier to run.
func looksLikeTOTPCode(code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// sealTOTPSecret envelopes the secret for at-rest storage; with no cipher
// (dev) it stores the raw base32 value (cmd/api warns at boot).
func (s *Service) sealTOTPSecret(secret string) (string, error) {
	if s.mfaCipher == nil {
		return secret, nil
	}
	return s.mfaCipher.Seal([]byte(secret))
}

// openTOTPSecret reverses sealTOTPSecret, tolerating raw dev-era values. A
// sealed value with no cipher configured is an operator error, not a code
// problem.
func (s *Service) openTOTPSecret(stored string) (string, error) {
	if !secretbox.IsSealed(stored) {
		return stored, nil
	}
	if s.mfaCipher == nil {
		return "", errors.New("auth: totp secret is sealed but no MFA KEK is configured")
	}
	raw, err := s.mfaCipher.Open(stored)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// generateRecoveryCode mints one recovery code: 40 bits of entropy rendered as
// 10 lowercase hex chars, dash-separated for readability ("a1b2c-3d4e5").
func generateRecoveryCode() (string, error) {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	h := hex.EncodeToString(b)
	return h[:5] + "-" + h[5:], nil
}

// hashRecoveryCode returns the SHA-256 hex of the canonicalized code (spaces
// trimmed, dashes stripped, lowercased) so user re-entry formatting never
// matters. Recovery codes are high-entropy random, so a fast hash is correct
// (same reasoning as refresh tokens).
func hashRecoveryCode(code string) string {
	canon := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	sum := sha256.Sum256([]byte(canon))
	return hex.EncodeToString(sum[:])
}
