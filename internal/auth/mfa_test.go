package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pquerna/otp/totp"

	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeMFARepo is an in-memory MFARepository mirroring the user_mfa /
// mfa_recovery_codes semantics (incl. the upsert's WHERE NOT enabled guard and
// UseRecoveryCode's single-use execrows contract).
type fakeMFARepo struct {
	rows  map[uuid.UUID]*sqlcgen.UserMfa
	codes map[uuid.UUID][]*sqlcgen.MfaRecoveryCode
}

func newFakeMFARepo() *fakeMFARepo {
	return &fakeMFARepo{
		rows:  map[uuid.UUID]*sqlcgen.UserMfa{},
		codes: map[uuid.UUID][]*sqlcgen.MfaRecoveryCode{},
	}
}

func (f *fakeMFARepo) UpsertUserMFA(_ context.Context, a sqlcgen.UpsertUserMFAParams) (sqlcgen.UserMfa, error) {
	if row, ok := f.rows[a.UserID]; ok {
		if row.Enabled {
			return sqlcgen.UserMfa{}, errors.New("no rows") // WHERE NOT enabled guard
		}
		row.TotpSecretSealed = a.TotpSecretSealed
		row.CreatedAt = time.Now()
		return *row, nil
	}
	row := &sqlcgen.UserMfa{UserID: a.UserID, TotpSecretSealed: a.TotpSecretSealed, CreatedAt: time.Now()}
	f.rows[a.UserID] = row
	return *row, nil
}

func (f *fakeMFARepo) GetUserMFA(_ context.Context, userID uuid.UUID) (sqlcgen.UserMfa, error) {
	if row, ok := f.rows[userID]; ok {
		return *row, nil
	}
	return sqlcgen.UserMfa{}, errors.New("not found")
}

func (f *fakeMFARepo) EnableUserMFA(_ context.Context, userID uuid.UUID) (int64, error) {
	row, ok := f.rows[userID]
	if !ok || row.Enabled {
		return 0, nil
	}
	row.Enabled = true
	return 1, nil
}

func (f *fakeMFARepo) DeleteUserMFA(_ context.Context, userID uuid.UUID) (int64, error) {
	if _, ok := f.rows[userID]; !ok {
		return 0, nil
	}
	delete(f.rows, userID)
	return 1, nil
}

func (f *fakeMFARepo) CreateRecoveryCode(_ context.Context, a sqlcgen.CreateRecoveryCodeParams) error {
	f.codes[a.UserID] = append(f.codes[a.UserID], &sqlcgen.MfaRecoveryCode{
		ID: uuid.New(), UserID: a.UserID, CodeHash: a.CodeHash, CreatedAt: time.Now(),
	})
	return nil
}

func (f *fakeMFARepo) DeleteRecoveryCodes(_ context.Context, userID uuid.UUID) error {
	delete(f.codes, userID)
	return nil
}

func (f *fakeMFARepo) UseRecoveryCode(_ context.Context, a sqlcgen.UseRecoveryCodeParams) (int64, error) {
	for _, c := range f.codes[a.UserID] {
		if c.CodeHash == a.CodeHash && !c.UsedAt.Valid {
			c.UsedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			return 1, nil
		}
	}
	return 0, nil
}

func (f *fakeMFARepo) CountUnusedRecoveryCodes(_ context.Context, userID uuid.UUID) (int64, error) {
	var n int64
	for _, c := range f.codes[userID] {
		if !c.UsedAt.Valid {
			n++
		}
	}
	return n, nil
}

// newMFAService builds an auth service with MFA wired to a fake repo. cipher
// may be nil (dev raw-storage mode).
func newMFAService(t *testing.T, cipher *secretbox.Cipher) (*Service, *fakeRepo, *fakeMFARepo) {
	t.Helper()
	repo := newFakeRepo()
	mfaRepo := newFakeMFARepo()
	svc := NewService(repo, newTestIssuer(), time.Hour, WithMFA(mfaRepo, cipher, "Vidra Test"))
	return svc, repo, mfaRepo
}

// totpCode computes the RFC 6238 code for secret at tm, failing the test on error.
func totpCode(t *testing.T, secret string, tm time.Time) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, tm)
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	return code
}

func TestTOTPEnrollVerifyAndMFALogin(t *testing.T) {
	ctx := context.Background()
	svc, _, mfaRepo := newMFAService(t, nil)
	user, _ := register(t, svc, "ada", "ada@example.test")

	// Login before enrollment is unchanged: tokens, no challenge.
	res, err := svc.Login(ctx, LoginInput{Email: "ada@example.test", Password: "supersecret"}, "ua")
	if err != nil || res.MFARequired || res.Tokens.AccessToken == "" {
		t.Fatalf("pre-MFA login = %+v, %v; want plain tokens", res, err)
	}

	enr, err := svc.BeginTOTPEnrollment(ctx, user.ID)
	if err != nil {
		t.Fatalf("BeginTOTPEnrollment: %v", err)
	}
	if enr.Secret == "" || !strings.HasPrefix(enr.URI, "otpauth://totp/") {
		t.Fatalf("enrollment = %+v; want secret + otpauth URI", enr)
	}
	if !strings.Contains(enr.URI, "Vidra%20Test") && !strings.Contains(enr.URI, "Vidra+Test") {
		t.Errorf("URI %q should carry the configured issuer label", enr.URI)
	}
	// Pending enrollment: login still unchanged.
	if res, err := svc.Login(ctx, LoginInput{Email: "ada@example.test", Password: "supersecret"}, "ua"); err != nil || res.MFARequired {
		t.Fatalf("pending-enrollment login = %+v, %v; want no challenge", res, err)
	}

	// A wrong code does not enable.
	if _, err := svc.VerifyTOTPEnrollment(ctx, user.ID, "000000"); !errors.Is(err, ErrInvalidMFACode) {
		t.Fatalf("wrong-code verify err = %v, want ErrInvalidMFACode", err)
	}
	codes, err := svc.VerifyTOTPEnrollment(ctx, user.ID, totpCode(t, enr.Secret, time.Now()))
	if err != nil {
		t.Fatalf("VerifyTOTPEnrollment: %v", err)
	}
	if len(codes) != recoveryCodeCount {
		t.Fatalf("got %d recovery codes, want %d", len(codes), recoveryCodeCount)
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if seen[c] {
			t.Errorf("duplicate recovery code %q", c)
		}
		seen[c] = true
	}
	// Only hashes are stored.
	for _, rc := range mfaRepo.codes[user.ID] {
		if seen[rc.CodeHash] {
			t.Error("a raw recovery code was persisted")
		}
	}
	if st, err := svc.GetMFAStatus(ctx, user.ID); err != nil || !st.Enabled || st.RecoveryCodesRemaining != recoveryCodeCount {
		t.Fatalf("status = %+v, %v; want enabled with %d codes", st, err, recoveryCodeCount)
	}

	// Login now withholds the session and returns an mfa_token.
	res, err = svc.Login(ctx, LoginInput{Email: "ada@example.test", Password: "supersecret"}, "ua")
	if err != nil {
		t.Fatalf("MFA login: %v", err)
	}
	if !res.MFARequired || res.MFAToken == "" || res.Tokens.AccessToken != "" || res.Tokens.RefreshToken != "" {
		t.Fatalf("MFA login = %+v; want mfa_token and no session tokens", res)
	}

	// The challenge completes with a real TOTP code.
	got, tokens, method, err := svc.CompleteMFAChallenge(ctx, res.MFAToken, totpCode(t, enr.Secret, time.Now()), "ua")
	if err != nil {
		t.Fatalf("CompleteMFAChallenge: %v", err)
	}
	if got.ID != user.ID || tokens.AccessToken == "" || tokens.RefreshToken == "" || method != MFAMethodTOTP {
		t.Errorf("challenge result = user %s, tokens %+v, method %q", got.ID, tokens, method)
	}
	// The minted access token is a REAL access token (normal audience).
	if _, err := svc.Parse(tokens.AccessToken); err != nil {
		t.Errorf("challenge access token should parse: %v", err)
	}
}

func TestTOTPSecretSealedAtRest(t *testing.T) {
	ctx := context.Background()
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i)
	}
	cipher, err := secretbox.NewCipher(kek)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	svc, _, mfaRepo := newMFAService(t, cipher)
	user, _ := register(t, svc, "ada", "ada@example.test")

	enr, err := svc.BeginTOTPEnrollment(ctx, user.ID)
	if err != nil {
		t.Fatalf("BeginTOTPEnrollment: %v", err)
	}
	stored := mfaRepo.rows[user.ID].TotpSecretSealed
	if !secretbox.IsSealed(stored) {
		t.Fatalf("stored secret %q is not sealed", stored)
	}
	if strings.Contains(stored, enr.Secret) {
		t.Error("sealed value leaks the raw secret")
	}
	// Verification still works through the seal.
	if _, err := svc.VerifyTOTPEnrollment(ctx, user.ID, totpCode(t, enr.Secret, time.Now())); err != nil {
		t.Fatalf("verify through sealed secret: %v", err)
	}
}

func TestTOTPSkewWindow(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newMFAService(t, nil)
	user, _ := register(t, svc, "ada", "ada@example.test")

	// Pin the service clock so window arithmetic is deterministic.
	at := time.Date(2026, 7, 3, 12, 0, 15, 0, time.UTC)
	svc.now = func() time.Time { return at }
	svc.mfaTokens.now = svc.now

	enr, err := svc.BeginTOTPEnrollment(ctx, user.ID)
	if err != nil {
		t.Fatalf("BeginTOTPEnrollment: %v", err)
	}
	// ±1 step (30s) verifies; ±2 steps does not.
	if _, err := svc.VerifyTOTPEnrollment(ctx, user.ID, totpCode(t, enr.Secret, at.Add(-30*time.Second))); err != nil {
		t.Fatalf("code from previous window should verify (skew 1): %v", err)
	}
	// MFA is now enabled — exercise the challenge path for the far-window cases.
	res, err := svc.Login(ctx, LoginInput{Email: "ada@example.test", Password: "supersecret"}, "ua")
	if err != nil || !res.MFARequired {
		t.Fatalf("login = %+v, %v; want MFA challenge", res, err)
	}
	if _, _, _, err := svc.CompleteMFAChallenge(ctx, res.MFAToken, totpCode(t, enr.Secret, at.Add(-90*time.Second)), "ua"); !errors.Is(err, ErrInvalidMFACode) {
		t.Errorf("code from -3 windows err = %v, want ErrInvalidMFACode", err)
	}
	if _, _, _, err := svc.CompleteMFAChallenge(ctx, res.MFAToken, totpCode(t, enr.Secret, at.Add(30*time.Second)), "ua"); err != nil {
		t.Errorf("code from next window should verify (skew 1): %v", err)
	}
}

func TestRecoveryCodeChallengeIsSingleUse(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newMFAService(t, nil)
	user, _ := register(t, svc, "ada", "ada@example.test")

	enr, _ := svc.BeginTOTPEnrollment(ctx, user.ID)
	codes, err := svc.VerifyTOTPEnrollment(ctx, user.ID, totpCode(t, enr.Secret, time.Now()))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	login := func() string {
		res, err := svc.Login(ctx, LoginInput{Email: "ada@example.test", Password: "supersecret"}, "ua")
		if err != nil || !res.MFARequired {
			t.Fatalf("login = %+v, %v; want MFA challenge", res, err)
		}
		return res.MFAToken
	}

	// Redeem one recovery code (formatting-insensitive: uppercase, no dash).
	presented := strings.ToUpper(strings.ReplaceAll(codes[0], "-", ""))
	_, tokens, method, err := svc.CompleteMFAChallenge(ctx, login(), presented, "ua")
	if err != nil || tokens.AccessToken == "" || method != MFAMethodRecovery {
		t.Fatalf("recovery challenge = %+v, %q, %v; want tokens via recovery_code", tokens, method, err)
	}
	if st, _ := svc.GetMFAStatus(ctx, user.ID); st.RecoveryCodesRemaining != recoveryCodeCount-1 {
		t.Errorf("remaining = %d, want %d", st.RecoveryCodesRemaining, recoveryCodeCount-1)
	}
	// The same code is dead now.
	if _, _, _, err := svc.CompleteMFAChallenge(ctx, login(), codes[0], "ua"); !errors.Is(err, ErrInvalidMFACode) {
		t.Fatalf("reused recovery code err = %v, want ErrInvalidMFACode", err)
	}
}

func TestCompleteMFAChallengeRejectsBadTokens(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newMFAService(t, nil)
	user, _ := register(t, svc, "ada", "ada@example.test")
	enr, _ := svc.BeginTOTPEnrollment(ctx, user.ID)
	if _, err := svc.VerifyTOTPEnrollment(ctx, user.ID, totpCode(t, enr.Secret, time.Now())); err != nil {
		t.Fatalf("verify: %v", err)
	}
	code := totpCode(t, enr.Secret, time.Now())

	// Garbage token.
	if _, _, _, err := svc.CompleteMFAChallenge(ctx, "garbage", code, "ua"); !errors.Is(err, ErrInvalidMFAToken) {
		t.Errorf("garbage token err = %v, want ErrInvalidMFAToken", err)
	}
	// An ACCESS token must not pass as an mfa_token (audience scoping).
	access, err := svc.issuer.Issue(user.ID, "user")
	if err != nil {
		t.Fatalf("issue access token: %v", err)
	}
	if _, _, _, err := svc.CompleteMFAChallenge(ctx, access, code, "ua"); !errors.Is(err, ErrInvalidMFAToken) {
		t.Errorf("access-token-as-mfa_token err = %v, want ErrInvalidMFAToken", err)
	}
	// An expired mfa_token (same secret/issuer/audience, negative TTL).
	expired, err := NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra"+mfaAudienceSuffix, -time.Minute).Issue(user.ID, "user")
	if err != nil {
		t.Fatalf("issue expired token: %v", err)
	}
	if _, _, _, err := svc.CompleteMFAChallenge(ctx, expired, code, "ua"); !errors.Is(err, ErrInvalidMFAToken) {
		t.Errorf("expired token err = %v, want ErrInvalidMFAToken", err)
	}
	// And the mfa_token itself must not pass as an ACCESS token.
	res, err := svc.Login(ctx, LoginInput{Email: "ada@example.test", Password: "supersecret"}, "ua")
	if err != nil || !res.MFARequired {
		t.Fatalf("login = %+v, %v; want MFA challenge", res, err)
	}
	if _, err := svc.Parse(res.MFAToken); err == nil {
		t.Error("mfa_token must not validate as an access token")
	}
}

func TestDisableTOTPRequiresPasswordAndDropsEverything(t *testing.T) {
	ctx := context.Background()
	svc, _, mfaRepo := newMFAService(t, nil)
	user, _ := register(t, svc, "ada", "ada@example.test")
	enr, _ := svc.BeginTOTPEnrollment(ctx, user.ID)
	if _, err := svc.VerifyTOTPEnrollment(ctx, user.ID, totpCode(t, enr.Secret, time.Now())); err != nil {
		t.Fatalf("verify: %v", err)
	}

	if err := svc.DisableTOTP(ctx, user.ID, "wrong-password"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("wrong-password disable err = %v, want ErrInvalidPassword", err)
	}
	if st, _ := svc.GetMFAStatus(ctx, user.ID); !st.Enabled {
		t.Fatal("a failed disable must leave MFA on")
	}
	if err := svc.DisableTOTP(ctx, user.ID, "supersecret"); err != nil {
		t.Fatalf("DisableTOTP: %v", err)
	}
	if st, _ := svc.GetMFAStatus(ctx, user.ID); st.Enabled || st.RecoveryCodesRemaining != 0 {
		t.Errorf("post-disable status = %+v, want disabled with 0 codes", st)
	}
	if len(mfaRepo.codes[user.ID]) != 0 {
		t.Error("recovery-code rows must be deleted on disable")
	}
	// Login is a plain login again.
	if res, err := svc.Login(ctx, LoginInput{Email: "ada@example.test", Password: "supersecret"}, "ua"); err != nil || res.MFARequired {
		t.Errorf("post-disable login = %+v, %v; want plain tokens", res, err)
	}
	// Nothing left to disable.
	if err := svc.DisableTOTP(ctx, user.ID, "supersecret"); !errors.Is(err, ErrMFANotEnabled) {
		t.Errorf("second disable err = %v, want ErrMFANotEnabled", err)
	}
}

func TestBeginTOTPEnrollmentConflictsAndRestart(t *testing.T) {
	ctx := context.Background()
	svc, _, mfaRepo := newMFAService(t, nil)
	user, _ := register(t, svc, "ada", "ada@example.test")

	first, err := svc.BeginTOTPEnrollment(ctx, user.ID)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// Restarting a PENDING enrollment replaces the secret.
	second, err := svc.BeginTOTPEnrollment(ctx, user.ID)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if first.Secret == second.Secret {
		t.Error("restart must mint a fresh secret")
	}
	// The first (replaced) secret no longer verifies.
	if _, err := svc.VerifyTOTPEnrollment(ctx, user.ID, totpCode(t, first.Secret, time.Now())); !errors.Is(err, ErrInvalidMFACode) {
		t.Errorf("old-secret verify err = %v, want ErrInvalidMFACode", err)
	}
	if _, err := svc.VerifyTOTPEnrollment(ctx, user.ID, totpCode(t, second.Secret, time.Now())); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Enabled → begin conflicts; verify-again conflicts.
	if _, err := svc.BeginTOTPEnrollment(ctx, user.ID); !errors.Is(err, ErrMFAAlreadyEnabled) {
		t.Errorf("begin-when-enabled err = %v, want ErrMFAAlreadyEnabled", err)
	}
	if _, err := svc.VerifyTOTPEnrollment(ctx, user.ID, totpCode(t, second.Secret, time.Now())); !errors.Is(err, ErrMFAAlreadyEnabled) {
		t.Errorf("verify-when-enabled err = %v, want ErrMFAAlreadyEnabled", err)
	}
	if !mfaRepo.rows[user.ID].Enabled {
		t.Error("row should be enabled")
	}
}

func TestMFAUnavailableWithoutWiring(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(newFakeRepo()) // no WithMFA
	user, _ := register(t, svc, "ada", "ada@example.test")

	if _, err := svc.BeginTOTPEnrollment(ctx, user.ID); !errors.Is(err, ErrMFAUnavailable) {
		t.Errorf("begin err = %v, want ErrMFAUnavailable", err)
	}
	if _, err := svc.GetMFAStatus(ctx, user.ID); !errors.Is(err, ErrMFAUnavailable) {
		t.Errorf("status err = %v, want ErrMFAUnavailable", err)
	}
	// Login is completely unchanged.
	if res, err := svc.Login(ctx, LoginInput{Email: "ada@example.test", Password: "supersecret"}, "ua"); err != nil || res.MFARequired || res.Tokens.AccessToken == "" {
		t.Errorf("login = %+v, %v; want plain tokens", res, err)
	}
}
