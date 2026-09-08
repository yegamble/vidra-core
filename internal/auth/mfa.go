package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
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
	// ErrMFASecretUndecryptable means the stored TOTP secret is `enc:`
	// ciphertext the configured KEK cannot open — the signature of a database
	// restored WITHOUT the config archive that carries MFA_KEY_KEK, or with the
	// wrong one. It is an operator fault and never the user's, and it is the one
	// failure a restore can hide for as long as nobody with a second factor
	// tries to sign in (A37 close-out, finding A37-1).
	//
	// Every path that meets it must fail CLOSED and INDISTINGUISHABLY: the
	// caller sees exactly what a wrong code produces, because an answer that
	// singled this case out would tell an unauthenticated caller which accounts
	// an instance can no longer verify. The whole signal goes to the operator's
	// log instead. Recovery codes are SHA-256 hashes, not KEK-sealed, so they
	// keep working — the account is not locked out.
	ErrMFASecretUndecryptable = errors.New("auth: totp secret cannot be decrypted with the configured MFA KEK")
)

// MFASecretUndecryptableError names the account whose sealed TOTP secret could
// not be opened, so the HTTP layer can write ONE actionable operator line —
// which account, which failure class — while the response stays identical to a
// wrong code's.
//
// Cause is the cipher's own message ("cipher: message authentication failed",
// "secretbox: base64: illegal base64 data at input byte 7"), which names the
// shape of the failure and never carries the ciphertext or any key material.
type MFASecretUndecryptableError struct {
	UserID uuid.UUID
	Cause  string
}

func (e *MFASecretUndecryptableError) Error() string {
	return "auth: totp secret for " + e.UserID.String() + " cannot be decrypted with the configured MFA KEK: " + e.Cause
}

// Unwrap makes errors.Is(err, ErrMFASecretUndecryptable) true, so a caller that
// only needs the class does not have to know about this type.
func (e *MFASecretUndecryptableError) Unwrap() error { return ErrMFASecretUndecryptable }

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
	// BurnTOTPStep records the time step of an accepted code on an ENABLED
	// row and returns 0 rows when that step is not newer than the last one
	// accepted — the single-use guarantee (0134).
	BurnTOTPStep(ctx context.Context, arg sqlcgen.BurnTOTPStepParams) (int64, error)
	// BurnPendingTOTPStep is the same write for a row still pending
	// (enabled=FALSE), used when an enrollment is confirmed.
	BurnPendingTOTPStep(ctx context.Context, arg sqlcgen.BurnPendingTOTPStepParams) (int64, error)
	// ListRecentUserMFASecrets samples the newest stored secrets for the
	// boot-time KEK check (CheckMFAKEK).
	ListRecentUserMFASecrets(ctx context.Context, rowLimit int32) ([]sqlcgen.ListRecentUserMFASecretsRow, error)
}

// MFAKEKSample is how many user_mfa rows the boot-time KEK check reads. A
// sample and not a scan: every row was sealed by whatever KEK the instance held
// at the time, so a handful of the newest answers "is the configured KEK the
// one that sealed this database" as well as a full table would, and it costs one
// indexed read on a path that must not slow a boot down.
const MFAKEKSample = 20

// MFAKEKReport is what the boot-time check found. Counts only — never a user
// id, never a ciphertext — because it is rendered on an operator page and
// carried in a log line.
type MFAKEKReport struct {
	// Sampled is how many rows were read (0 = no account has ever enrolled).
	Sampled int
	// Sealed counts the sampled rows that are `enc:` ciphertext. A dev install
	// with no KEK stores raw secrets, and those are not a KEK question.
	Sealed int
	// Undecryptable counts the sealed rows the configured KEK could not open.
	// Anything above zero means at least one account's second factor can never
	// verify; Undecryptable == Sealed is the whole-database case — a restore
	// that came back without the config archive holding MFA_KEY_KEK.
	Undecryptable int
}

// Mismatch reports whether the configured KEK failed to open something it is
// supposed to own.
func (r MFAKEKReport) Mismatch() bool { return r.Undecryptable > 0 }

// CheckMFAKEK samples the newest stored TOTP secrets and reports how many the
// configured KEK cannot open (A37-2).
//
// THE FAILURE IT CLOSES. A wrong or missing MFA_KEY_KEK is not a startup error
// and not a probe failure: the key is validated for SHAPE (32 bytes of base64)
// and never against anything it sealed, so an api restored with the wrong one
// boots, answers /readyz 200, and serves every password login, HLS read and
// admin page exactly as before. The first symptom arrives whenever the first
// account with a second factor next tries to sign in, which on a small instance
// can be days.
//
// It is deliberately NOT fatal and deliberately not a readiness failure: an
// operator who has just restored with the wrong KEK needs to be able to log in
// with a password to fix it, and 503ing the api is the one thing that would
// stop them.
func (s *Service) CheckMFAKEK(ctx context.Context, sample int32) (MFAKEKReport, error) {
	var rep MFAKEKReport
	if s.mfaRepo == nil {
		return rep, nil
	}
	if sample <= 0 {
		sample = MFAKEKSample
	}
	rows, err := s.mfaRepo.ListRecentUserMFASecrets(ctx, sample)
	if err != nil {
		return rep, err
	}
	rep.Sampled = len(rows)
	for _, row := range rows {
		if !secretbox.IsSealed(row.TotpSecretSealed) {
			continue
		}
		rep.Sealed++
		if _, oerr := s.openTOTPSecret(row.UserID, row.TotpSecretSealed); oerr != nil {
			rep.Undecryptable++
		}
	}
	return rep, nil
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
	secret, err := s.openTOTPSecret(userID, row.TotpSecretSealed)
	if err != nil {
		return nil, err
	}
	step, ok := s.matchTOTPStep(code, secret)
	if !ok {
		return nil, ErrInvalidMFACode
	}
	// Burn the confirming code BEFORE enabling: the code that switched
	// two-factor on must not then be spendable on a login challenge, and doing
	// it first means a burn that loses its race cannot leave MFA enabled by a
	// code it also rejected.
	burned, err := s.mfaRepo.BurnPendingTOTPStep(ctx, sqlcgen.BurnPendingTOTPStepParams{UserID: userID, Step: step})
	if err != nil {
		return nil, err
	}
	if burned == 0 {
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
//
// Removing the second factor lowers the account's protection, so it does what a
// password change does: every OTHER session is revoked and the account is
// mailed a notice. The session that made the change stays signed in. Access
// tokens are session-bound, so the revocation reaches the other devices within
// one request rather than within JWT_ACCESS_TTL. Before this, an attacker who
// held a session AND the password could strip 2FA and leave every session they
// had planted alive and unmentioned.
func (s *Service) DisableTOTP(ctx context.Context, userID uuid.UUID, password, currentSessionID string) error {
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
	if err := s.mfaRepo.DeleteRecoveryCodes(ctx, userID); err != nil {
		return err
	}
	s.afterTwoFactorRemoved(ctx, user, currentSessionID, false)
	return nil
}

// AdminRemoveTOTP is the operator's answer to "I lost my phone and my recovery
// codes". It removes a target account's second factor after re-verifying the
// ADMINISTRATOR's own password — the acting party is the one who must prove
// possession, since the whole point is that the target cannot.
//
// It never reads, returns or re-issues anything secret: the TOTP secret and the
// recovery codes are deleted, not disclosed, so an admin cannot use this to
// impersonate a user's second factor. The target's sessions are revoked (an
// account whose protection just changed should re-authenticate) and the target
// is mailed the notice — the only signal that reaches a user whose second factor
// was removed by somebody else. Self-reset is ALLOWED and audited: the owner may
// lock themselves out too, and there is nobody above them to ask; in that one
// case the acting session survives, exactly as it does for a self-service
// removal.
func (s *Service) AdminRemoveTOTP(ctx context.Context, adminID uuid.UUID, adminPassword string, targetID uuid.UUID, adminSessionID string) error {
	if s.mfaRepo == nil {
		return ErrMFAUnavailable
	}
	admin, err := s.UserByID(ctx, adminID)
	if err != nil {
		return err
	}
	if admin.PasswordHash == "" {
		return ErrPasswordNotSet
	}
	if err := CheckPassword(admin.PasswordHash, adminPassword); err != nil {
		return ErrInvalidPassword
	}
	target, err := s.UserByID(ctx, targetID)
	if err != nil {
		return ErrAccountNotFound
	}
	n, err := s.mfaRepo.DeleteUserMFA(ctx, targetID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrMFANotEnabled
	}
	if err := s.mfaRepo.DeleteRecoveryCodes(ctx, targetID); err != nil {
		return err
	}
	sessionToKeep := ""
	if targetID == adminID {
		sessionToKeep = adminSessionID
	}
	s.afterTwoFactorRemoved(ctx, target, sessionToKeep, targetID != adminID)
	return nil
}

// afterTwoFactorRemoved is the shared tail of both removal paths: revoke the
// account's sessions (all of them, unless one is the caller's own to keep) and
// mail the notice. Both are best-effort — the second factor is already gone, and
// reporting a failure the user would read as "2FA is still on" would be a lie.
func (s *Service) afterTwoFactorRemoved(ctx context.Context, user sqlcgen.User, sessionToKeep string, byAdmin bool) {
	if sessionID, perr := uuid.Parse(sessionToKeep); perr == nil {
		_ = s.repo.RevokeOtherUserSessions(ctx, sqlcgen.RevokeOtherUserSessionsParams{
			UserID: user.ID,
			ID:     sessionID,
		})
	} else {
		_ = s.repo.RevokeAllUserSessions(ctx, user.ID)
	}
	_ = s.mailer.SendTwoFactorRemoved(ctx, user.Email, byAdmin)
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
		secret, err := s.openTOTPSecret(userID, row.TotpSecretSealed)
		if err != nil {
			return sqlcgen.User{}, Tokens{}, "", err
		}
		step, ok := s.matchTOTPStep(code, secret)
		if !ok {
			return sqlcgen.User{}, Tokens{}, "", ErrInvalidMFACode
		}
		// Single-use (0134): the same statement that records the accepted step
		// refuses one that is not newer, so a replayed code — the code itself,
		// or an older one still inside the ±1 skew — is indistinguishable from
		// a wrong code, right down to the status the caller sees and the
		// challenge limiter's count.
		burned, err := s.mfaRepo.BurnTOTPStep(ctx, sqlcgen.BurnTOTPStepParams{UserID: userID, Step: step})
		if err != nil {
			return sqlcgen.User{}, Tokens{}, "", err
		}
		if burned == 0 {
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

// matchTOTPStep checks an RFC 6238 code against the base32 secret at the
// service clock, allowing ±1 period of skew, and reports WHICH time step it
// matched. The step is what makes a code burnable (0134): "this code was
// accepted" is only actionable if the server can say which 30-second window it
// belonged to. Candidates are tried newest-first so a code that is valid at two
// steps (it cannot be, but the loop must not depend on that) burns the later.
//
// The comparison is constant-time — the presented value is a credential, and
// the house rule for credential comparison is subtle.ConstantTimeCompare.
func (s *Service) matchTOTPStep(code, secret string) (int64, bool) {
	code = strings.TrimSpace(code)
	period := int64(totpValidateOpts.Period)
	if period <= 0 {
		return 0, false
	}
	base := s.now().Unix() / period
	for _, step := range []int64{base + 1, base, base - 1} {
		want, err := totp.GenerateCodeCustom(secret, time.Unix(step*period, 0), totpValidateOpts)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// validTOTPCode reports whether the code verifies at all, ignoring replay. It
// is the ±1-skew arithmetic and nothing else; every caller that ACCEPTS a code
// must also burn its step.
func (s *Service) validTOTPCode(code, secret string) bool {
	_, ok := s.matchTOTPStep(code, secret)
	return ok
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

// openTOTPSecret reverses sealTOTPSecret, tolerating raw dev-era values.
//
// Both ways of failing are the SAME operator fault — the KEK that sealed this
// row is not the KEK this process holds — so both return the one sentinel the
// HTTP layer knows to answer with a wrong-code refusal. Returning a bare error
// here is what made the challenge 500 on a wrong-KEK restore (A37-1): an
// unclassified error reaches echo's handler, which has no choice but to call it
// internal.
func (s *Service) openTOTPSecret(userID uuid.UUID, stored string) (string, error) {
	if !secretbox.IsSealed(stored) {
		return stored, nil
	}
	if s.mfaCipher == nil {
		return "", &MFASecretUndecryptableError{
			UserID: userID,
			Cause:  "the stored secret is sealed but no MFA KEK is configured (MFA_KEY_KEK, or a shared FEDERATION_KEY_KEK)",
		}
	}
	raw, err := s.mfaCipher.Open(stored)
	if err != nil {
		return "", &MFASecretUndecryptableError{UserID: userID, Cause: err.Error()}
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
