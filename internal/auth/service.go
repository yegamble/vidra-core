package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sentinel errors the HTTP layer maps to status codes. They never carry
// sensitive detail.
var (
	// ErrConflict means the username or email is already taken.
	ErrConflict = errors.New("auth: username or email already taken")
	// ErrInvalidCredentials is returned for both unknown account and wrong
	// password, so callers cannot probe which emails exist.
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	// ErrAccountDisabled means the account exists but is deactivated.
	ErrAccountDisabled = errors.New("auth: account is disabled")
	// ErrInvalidRefresh means the refresh token is unknown, revoked, or expired.
	ErrInvalidRefresh = errors.New("auth: invalid or expired refresh token")
	// ErrInvalidPassword means a password confirmation (e.g. for a sensitive
	// self-service action) did not match the account's password.
	ErrInvalidPassword = errors.New("auth: incorrect password")
	// ErrEmailVerificationRequired means the account was created while the
	// registration email-verification gate was active and its email is still
	// unverified — the session is withheld until the verification link is
	// followed (config-parity W7).
	ErrEmailVerificationRequired = errors.New("auth: email verification required")
	// ErrOwnerClaimRequired means the instance is awaiting its owner: the users
	// table is empty and an unclaimed owner-claim token exists, so every normal
	// signup path refuses until POST /setup/claim-owner creates the admin.
	ErrOwnerClaimRequired = errors.New("auth: owner claim required")
	// ErrOwnerClaimInvalid means the presented owner-claim token is wrong,
	// already redeemed, or no claim is pending. Deliberately one error for all
	// three so the endpoint is non-probing.
	ErrOwnerClaimInvalid = errors.New("auth: invalid or already-used owner-claim token")
)

// Repository is the data access the auth service needs. *sqlcgen.Queries
// satisfies it directly, so the production wiring is a one-liner and tests can
// substitute an in-memory fake.
type Repository interface {
	CreateUser(ctx context.Context, arg sqlcgen.CreateUserParams) (sqlcgen.User, error)
	GetUserByEmail(ctx context.Context, lowerEmail string) (sqlcgen.User, error)
	// GetUserByLoginIdentifier resolves a sign-in identifier that may be an
	// email OR a username, in one round trip, email taking precedence. It is
	// the ONLY lookup Login may use: unlike GetUserByUsername it does not
	// filter on is_active, so the disabled check stays after the password
	// compare and cannot become an enumeration oracle.
	GetUserByLoginIdentifier(ctx context.Context, lowerIdentifier string) (sqlcgen.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error)
	GetPublicUserProfileByUsername(ctx context.Context, lowerUsername string) (sqlcgen.GetPublicUserProfileByUsernameRow, error)
	// Public account search (GET /api/v1/search/accounts) — the list form of
	// GetPublicUserProfileByUsername's visibility rule.
	SearchPublicAccounts(ctx context.Context, arg sqlcgen.SearchPublicAccountsParams) ([]sqlcgen.SearchPublicAccountsRow, error)
	CountSearchPublicAccounts(ctx context.Context, arg sqlcgen.CountSearchPublicAccountsParams) (int64, error)
	CountUsers(ctx context.Context) (int64, error)
	UpdateUserProfile(ctx context.Context, arg sqlcgen.UpdateUserProfileParams) (sqlcgen.User, error)
	DeactivateUser(ctx context.Context, id uuid.UUID) error

	CreateSession(ctx context.Context, arg sqlcgen.CreateSessionParams) (sqlcgen.CreateSessionRow, error)
	GetSessionByRefreshHash(ctx context.Context, refreshHash string) (sqlcgen.GetSessionByRefreshHashRow, error)
	RevokeSession(ctx context.Context, id uuid.UUID) error
	RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error

	CreatePasswordResetToken(ctx context.Context, arg sqlcgen.CreatePasswordResetTokenParams) (sqlcgen.PasswordResetToken, error)
	GetPasswordResetToken(ctx context.Context, tokenHash string) (sqlcgen.PasswordResetToken, error)
	MarkPasswordResetTokenUsed(ctx context.Context, id uuid.UUID) error
	DeleteUnusedPasswordResetTokens(ctx context.Context, userID uuid.UUID) error
	UpdateUserPassword(ctx context.Context, arg sqlcgen.UpdateUserPasswordParams) error

	CreateEmailVerificationToken(ctx context.Context, arg sqlcgen.CreateEmailVerificationTokenParams) (sqlcgen.EmailVerificationToken, error)
	GetEmailVerificationToken(ctx context.Context, tokenHash string) (sqlcgen.EmailVerificationToken, error)
	MarkEmailVerificationTokenUsed(ctx context.Context, id uuid.UUID) error
	DeleteUnusedEmailVerificationTokens(ctx context.Context, userID uuid.UUID) error
	SetUserEmailVerified(ctx context.Context, id uuid.UUID) error

	CreateRegistrationRequest(ctx context.Context, arg sqlcgen.CreateRegistrationRequestParams) (sqlcgen.CreateRegistrationRequestRow, error)
	ListRegistrationRequests(ctx context.Context, arg sqlcgen.ListRegistrationRequestsParams) ([]sqlcgen.ListRegistrationRequestsRow, error)
	CountRegistrationRequests(ctx context.Context, status *string) (int64, error)
	ApproveRegistrationRequest(ctx context.Context, arg sqlcgen.ApproveRegistrationRequestParams) (sqlcgen.ApproveRegistrationRequestRow, error)
	RejectRegistrationRequest(ctx context.Context, arg sqlcgen.RejectRegistrationRequestParams) (int64, error)

	// Owner-claim bootstrap (0104) — see ownerclaim.go.
	UpsertOwnerClaimToken(ctx context.Context, tokenHash string) (sqlcgen.OwnerClaimToken, error)
	GetUnclaimedOwnerClaimToken(ctx context.Context) (sqlcgen.OwnerClaimToken, error)
	ClaimOwnerAndCreateAdmin(ctx context.Context, arg sqlcgen.ClaimOwnerAndCreateAdminParams) (sqlcgen.ClaimOwnerAndCreateAdminRow, error)
}

// defaultResetTTL is how long a password-reset token stays valid.
const defaultResetTTL = time.Hour

// defaultVerifyTTL is how long an email-verification token stays valid. It is
// longer than a reset token because a new user may not check email immediately.
const defaultVerifyTTL = 24 * time.Hour

// Service holds the auth application logic.
type Service struct {
	repo       Repository
	issuer     *TokenIssuer
	refreshTTL time.Duration
	resetTTL   time.Duration
	verifyTTL  time.Duration
	mailer     Mailer
	now        func() time.Time // injectable clock for tests

	// newUserHistoryFn resolves the new_user_history_enabled instance setting
	// (config-parity W7): the watch-history preference seeded onto accounts at
	// creation. nil = seed true (the shipped behaviour).
	newUserHistoryFn func() bool
	// verificationGateFn reports whether the registration email-verification
	// gate is EFFECTIVE right now (registration_require_email_verification AND
	// an outbound mail path exists). nil = gate off. Consulted at registration
	// (hold the session, mark the account pending) and at login (refuse a
	// still-pending account). The check is against the CURRENT setting, so
	// turning the gate off releases held accounts; accounts created while it
	// was off are never retroactively locked (their pending flag is false —
	// the grandfather clause).
	verificationGateFn func() bool

	// fixedOwnerClaimToken pins owner-claim mints to a deterministic value
	// (WithFixedOwnerClaimToken — dev/test-only). "" = random mint.
	fixedOwnerClaimToken string

	// TOTP MFA collaborators (WithMFA). mfaRepo nil = feature not wired: the
	// MFA endpoints answer ErrMFAUnavailable and login is unchanged.
	mfaRepo       MFARepository
	mfaCipher     *secretbox.Cipher // nil in dev → secrets stored raw
	mfaIssuerName string            // otpauth:// issuer label
	mfaTokens     *TokenIssuer      // single-purpose mfa_token minting/parsing
}

// NewService builds the auth service. refreshTTL is the refresh-token lifetime.
// Optional behavior (a real password-reset mailer, a custom reset-token TTL) is
// supplied via Options; the defaults are a no-op mailer and a 1h reset TTL.
func NewService(repo Repository, issuer *TokenIssuer, refreshTTL time.Duration, opts ...Option) *Service {
	s := &Service{
		repo:       repo,
		issuer:     issuer,
		refreshTTL: refreshTTL,
		resetTTL:   defaultResetTTL,
		verifyTTL:  defaultVerifyTTL,
		mailer:     noopMailer{},
		now:        time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Option configures optional Service behavior at construction time.
type Option func(*Service)

// WithMailer injects a concrete mailer for account-security messages
// (password reset, email verification). Default: a no-op that drops the
// message. A nil mailer is ignored.
func WithMailer(m Mailer) Option {
	return func(s *Service) {
		if m != nil {
			s.mailer = m
		}
	}
}

// WithResetTTL overrides the password-reset token lifetime (default 1h). A
// non-positive duration is ignored.
func WithResetTTL(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.resetTTL = d
		}
	}
}

// WithNewUserHistoryEnabledFunc wires the live new_user_history_enabled
// instance setting: f is consulted at account creation to seed the per-user
// watch-history preference. nil is ignored (seed true).
func WithNewUserHistoryEnabledFunc(f func() bool) Option {
	return func(s *Service) {
		if f != nil {
			s.newUserHistoryFn = f
		}
	}
}

// WithEmailVerificationGateFunc wires the EFFECTIVE registration
// email-verification gate (registration_require_email_verification AND mail
// wired — cmd/api folds both in). nil is ignored (gate off).
func WithEmailVerificationGateFunc(f func() bool) Option {
	return func(s *Service) {
		if f != nil {
			s.verificationGateFn = f
		}
	}
}

// newUserHistoryEnabled resolves the history-preference seed for a new account.
func (s *Service) newUserHistoryEnabled() bool {
	if s.newUserHistoryFn != nil {
		return s.newUserHistoryFn()
	}
	return true
}

// EmailVerificationGateActive reports whether the registration
// email-verification gate is effective right now.
func (s *Service) EmailVerificationGateActive() bool {
	return s.verificationGateFn != nil && s.verificationGateFn()
}

// Tokens is the access + refresh pair returned by register/login/refresh.
type Tokens struct {
	AccessToken  string
	RefreshToken string
}

// issueTokens mints an access token and a persisted, rotating refresh token for
// the user. The raw refresh token is returned to the caller exactly once; only
// its hash is stored.
func (s *Service) issueTokens(ctx context.Context, user sqlcgen.User, userAgent string) (Tokens, error) {
	access, err := s.issuer.Issue(user.ID, user.Role)
	if err != nil {
		return Tokens{}, err
	}
	raw, hash, err := generateRefreshToken()
	if err != nil {
		return Tokens{}, err
	}
	if _, err := s.repo.CreateSession(ctx, sqlcgen.CreateSessionParams{
		UserID:      user.ID,
		RefreshHash: hash,
		UserAgent:   userAgent,
		ExpiresAt:   s.now().Add(s.refreshTTL),
	}); err != nil {
		return Tokens{}, err
	}
	return Tokens{AccessToken: access, RefreshToken: raw}, nil
}

// RegisterInput is validated, normalized registration data.
type RegisterInput struct {
	Username string
	Email    string
	Password string
}

// LoginInput is validated login data. Sign-in accepts an email OR a username;
// Email is kept as the legacy field so existing callers compile unchanged.
type LoginInput struct {
	// Email is the email-only sign-in field. Used when Identifier is empty.
	Email string
	// Identifier is the email-or-username sign-in string. When non-empty it
	// takes precedence over Email — the HTTP layer 422s a body that sets both,
	// so exactly one is ever meaningful.
	Identifier string
	Password   string
}

// loginIdentifier collapses Email/Identifier to the single, trimmed string the
// account lookup is keyed by. Exactly one lookup per attempt is a security
// invariant, so the service never sees two candidate identifiers.
func (in LoginInput) loginIdentifier() string {
	if s := strings.TrimSpace(in.Identifier); s != "" {
		return s
	}
	return strings.TrimSpace(in.Email)
}

// Register creates an account and returns it with a fresh access + refresh token
// pair. Every account registers as "user" — the admin is created only via the
// owner-claim flow (ownerclaim.go), never by registering first. Username/email
// uniqueness is enforced by the database; a violation maps to ErrConflict.
func (s *Service) Register(ctx context.Context, in RegisterInput, userAgent string) (sqlcgen.User, Tokens, error) {
	if err := s.refuseIfOwnerUnclaimed(ctx); err != nil {
		return sqlcgen.User{}, Tokens{}, err
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, err
	}

	user, err := s.repo.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username:     strings.TrimSpace(in.Username),
		Email:        strings.TrimSpace(in.Email),
		PasswordHash: hash,
		Role:         "user",
		// Seed the per-user watch-history preference from the instance setting
		// (new_user_history_enabled, config-parity W7).
		HistoryEnabled: s.newUserHistoryEnabled(),
	})
	if err != nil {
		if pgconv.IsUniqueViolation(err) {
			return sqlcgen.User{}, Tokens{}, ErrConflict
		}
		return sqlcgen.User{}, Tokens{}, err
	}

	tokens, err := s.issueTokens(ctx, user, userAgent)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, err
	}
	return user, tokens, nil
}

// RegisterPendingVerification creates an account HELD behind the registration
// email-verification gate (config-parity W7): the account exists with
// pending_email_verification set, no session is issued, and a verification
// message is sent. Login is refused (ErrEmailVerificationRequired) until the
// link is followed. The caller decides when to use this instead of Register
// (the gate is effective). Like Register, the account is always "user" — the
// admin exists only via the owner-claim flow (ownerclaim.go).
func (s *Service) RegisterPendingVerification(ctx context.Context, in RegisterInput) (sqlcgen.User, error) {
	if err := s.refuseIfOwnerUnclaimed(ctx); err != nil {
		return sqlcgen.User{}, err
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return sqlcgen.User{}, err
	}

	user, err := s.repo.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username:                 strings.TrimSpace(in.Username),
		Email:                    strings.TrimSpace(in.Email),
		PasswordHash:             hash,
		Role:                     "user",
		PendingEmailVerification: true,
		HistoryEnabled:           s.newUserHistoryEnabled(),
	})
	if err != nil {
		if pgconv.IsUniqueViolation(err) {
			return sqlcgen.User{}, ErrConflict
		}
		return sqlcgen.User{}, err
	}
	// The verification send is returned ALONGSIDE the created user: a mailer
	// hiccup must not orphan the signup (a retry would 409 on the existing
	// account), so the HTTP layer logs the failure and still answers 202. The
	// operator recovery path is the admin email_verified override.
	if err := s.RequestEmailVerification(ctx, user.ID); err != nil {
		return user, err
	}
	return user, nil
}

// CountUsers reports the total number of accounts (the registration_user_limit
// gate's denominator; approximate under concurrent signups by design).
func (s *Service) CountUsers(ctx context.Context) (int64, error) {
	return s.repo.CountUsers(ctx)
}

// LoginResult is the outcome of a successful credential verification: either a
// completed session (Tokens) or, when the account has TOTP MFA enabled, a
// pending challenge — MFARequired with a short-lived single-purpose MFAToken
// and NO session tokens (the session is only issued by CompleteMFAChallenge).
type LoginResult struct {
	User        sqlcgen.User
	Tokens      Tokens
	MFARequired bool
	MFAToken    string
}

// Login verifies credentials presented as an email OR a username. Without MFA
// it returns the account with an access + refresh token pair; with TOTP enabled
// it withholds the session and returns an mfa_token instead (see LoginResult).
// Unknown account and wrong password are indistinguishable
// (ErrInvalidCredentials).
//
// SECURITY INVARIANTS — the lookup resolves at most ONE account (email column
// first, so the owner of an address is the only account that string can reach)
// and its password is the only one compared. A match with a wrong password is a
// flat ErrInvalidCredentials; there is deliberately no retry against the other
// column, which would let one identifier reach two accounts. A total miss still
// runs the dummy compare, and the disabled/verification/MFA gates all stay
// behind the compare so none of them answers before a credential is proven.
func (s *Service) Login(ctx context.Context, in LoginInput, userAgent string) (LoginResult, error) {
	user, err := s.repo.GetUserByLoginIdentifier(ctx, in.loginIdentifier())
	if err != nil {
		// Run a dummy compare to keep timing roughly constant whether or not the
		// account exists, reducing user-enumeration via response time.
		_ = CheckPassword("$2a$12$0000000000000000000000000000000000000000000000000000", in.Password)
		return LoginResult{}, ErrInvalidCredentials
	}
	if err := CheckPassword(user.PasswordHash, in.Password); err != nil {
		return LoginResult{}, ErrInvalidCredentials
	}
	if !user.IsActive {
		return LoginResult{}, ErrAccountDisabled
	}
	// Email-verification hold (config-parity W7): an account created while the
	// registration verification gate was active stays sessionless until its
	// email is verified. Checked against the CURRENT effective gate, so
	// disabling the setting releases held accounts; pre-gate accounts carry
	// pending=false and are never retroactively locked (grandfather clause).
	if user.PendingEmailVerification && !user.EmailVerified && s.EmailVerificationGateActive() {
		return LoginResult{}, ErrEmailVerificationRequired
	}

	// MFA gate: credentials alone do not make a session on an MFA-enabled
	// account — the caller must complete the TOTP/recovery-code challenge.
	if s.mfaEnabled(ctx, user.ID) {
		mfaToken, err := s.mfaTokens.Issue(user.ID, user.Role)
		if err != nil {
			return LoginResult{}, err
		}
		return LoginResult{User: user, MFARequired: true, MFAToken: mfaToken}, nil
	}

	tokens, err := s.issueTokens(ctx, user, userAgent)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{User: user, Tokens: tokens}, nil
}

// Refresh rotates a refresh token: it validates the presented token, revokes the
// old session, and issues a new access + refresh pair. Presenting an
// already-revoked token is treated as theft — all of that user's sessions are
// revoked and ErrInvalidRefresh is returned.
func (s *Service) Refresh(ctx context.Context, rawRefresh, userAgent string) (sqlcgen.User, Tokens, error) {
	sess, err := s.repo.GetSessionByRefreshHash(ctx, hashRefreshToken(rawRefresh))
	if err != nil {
		return sqlcgen.User{}, Tokens{}, ErrInvalidRefresh
	}
	if sess.RevokedAt.Valid {
		// Reuse of a rotated token — assume compromise and revoke everything.
		_ = s.repo.RevokeAllUserSessions(ctx, sess.UserID)
		return sqlcgen.User{}, Tokens{}, ErrInvalidRefresh
	}
	if !sess.ExpiresAt.After(s.now()) {
		return sqlcgen.User{}, Tokens{}, ErrInvalidRefresh
	}

	user, err := s.UserByID(ctx, sess.UserID)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, ErrInvalidRefresh
	}

	if err := s.repo.RevokeSession(ctx, sess.ID); err != nil {
		return sqlcgen.User{}, Tokens{}, err
	}
	tokens, err := s.issueTokens(ctx, user, userAgent)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, err
	}
	return user, tokens, nil
}

// Logout revokes the session for the presented refresh token. It is idempotent:
// an unknown or already-revoked token is a no-op (no error), so logout never
// leaks whether a token was valid.
func (s *Service) Logout(ctx context.Context, rawRefresh string) error {
	sess, err := s.repo.GetSessionByRefreshHash(ctx, hashRefreshToken(rawRefresh))
	if err != nil {
		return nil
	}
	return s.repo.RevokeSession(ctx, sess.ID)
}

// LogoutAll revokes every active session for a user (e.g. "sign out everywhere").
func (s *Service) LogoutAll(ctx context.Context, userID uuid.UUID) error {
	return s.repo.RevokeAllUserSessions(ctx, userID)
}

// ErrAccountNotFound means no active account matches the authenticated subject
// (e.g. a still-valid token for a since-deleted user).
var ErrAccountNotFound = errors.New("auth: account not found")

// Parse validates an access token and returns its claims. It is the entry point
// the HTTP auth middleware uses to authenticate a request.
func (s *Service) Parse(token string) (*Claims, error) {
	return s.issuer.Parse(token)
}

// UserByID loads the current account for an authenticated subject. A disabled
// account is treated as not found so a deactivated user cannot keep acting on a
// still-valid token.
func (s *Service) UserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error) {
	user, err := s.repo.GetUserByID(ctx, id)
	if err != nil {
		return sqlcgen.User{}, ErrAccountNotFound
	}
	if !user.IsActive {
		return sqlcgen.User{}, ErrAccountNotFound
	}
	return user, nil
}

// PublicProfileByUsername resolves only active accounts that explicitly opted
// into a public profile. The query intentionally makes private and unknown
// accounts indistinguishable to callers.
func (s *Service) PublicProfileByUsername(ctx context.Context, username string) (sqlcgen.GetPublicUserProfileByUsernameRow, error) {
	profile, err := s.repo.GetPublicUserProfileByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		return sqlcgen.GetPublicUserProfileByUsernameRow{}, ErrAccountNotFound
	}
	return profile, nil
}

// SearchPublicAccounts returns one page of PUBLICLY VISIBLE accounts whose
// username or display name contains query, plus the total under the same
// predicate.
//
// "Publicly visible" is not re-decided here. It is the rule
// GetPublicUserProfileByUsername already enforces — active AND profile_public —
// expressed once in SQL so the list and the profile lookup cannot disagree,
// plus the account-level discovery opt-out (unlisted) that a search result
// list, unlike a direct profile URL, has to honour. See SearchPublicAccounts in
// queries/users.sql for the full reasoning; the important property is that an
// account absent from GET /users/{username}/profile is absent from here too.
//
// viewerAuthed=false means an anonymous caller, for whom the per-viewer
// mute/block predicates are no-ops. The caller validates/clamps query, limit,
// and offset.
func (s *Service) SearchPublicAccounts(ctx context.Context, query string, viewerID uuid.UUID, viewerAuthed bool, limit, offset int32) ([]sqlcgen.SearchPublicAccountsRow, int64, error) {
	viewer := pgtype.UUID{Bytes: viewerID, Valid: viewerAuthed}
	rows, err := s.repo.SearchPublicAccounts(ctx, sqlcgen.SearchPublicAccountsParams{
		Query:        strings.TrimSpace(query),
		ViewerID:     viewer,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountSearchPublicAccounts(ctx, sqlcgen.CountSearchPublicAccountsParams{
		Query:    strings.TrimSpace(query),
		ViewerID: viewer,
	})
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ProfileInput is a partial account-profile update: nil fields are unchanged.
type ProfileInput struct {
	DisplayName *string
	Bio         *string
	// Unlisted toggles the account-level discovery opt-out (product-decisions
	// §16): when true, the account's channels/videos are excluded from public
	// discovery surfaces while direct URLs keep serving.
	Unlisted *bool
	// HistoryEnabled toggles the per-user watch-history preference (config-
	// parity W7): while false, watch-progress/history writes are skipped.
	HistoryEnabled *bool
	// ProfilePublic controls whether GET /users/{username}/profile exists.
	ProfilePublic *bool
	// ShowBluesky toggles displaying the account's linked Bluesky/ATProto handle
	// on its public profile (0102). nil leaves it unchanged; default false.
	ShowBluesky *bool
	// Search & recommendation preferences (search-service W4): the user half of
	// the two-factor personalization gate. nil leaves each unchanged.
	SearchHistoryEnabled               *bool
	PersonalizedSearchEnabled          *bool
	PersonalizedRecommendationsEnabled *bool
	// SensitiveContentPolicy is the per-user sensitive-content policy override
	// (0100): nil leaves it unchanged; a non-nil value sets it, where the empty
	// string clears the override (inherit the instance policy) and a non-empty
	// value (validated by the HTTP layer against the four enum strings) sets it.
	SensitiveContentPolicy *string
}

// UpdateProfile updates the authenticated account's presentation fields
// (display name, bio, unlisted flag). Identity fields (username, email) are
// intentionally not changed here — those need their own re-verification flow.
func (s *Service) UpdateProfile(ctx context.Context, id uuid.UUID, in ProfileInput) (sqlcgen.User, error) {
	// Sensitive-content policy is tri-state (0100): unchanged unless the caller
	// provided the field, in which case a trimmed enum value sets the override and
	// an empty string clears it to NULL (inherit). trimPtr already trims; a nil
	// pointer after trimming means "clear to NULL".
	setSensitive := in.SensitiveContentPolicy != nil
	var sensitiveVal *string
	if trimmed := pgconv.TrimPtr(in.SensitiveContentPolicy); trimmed != nil && *trimmed != "" {
		sensitiveVal = trimmed
	}
	user, err := s.repo.UpdateUserProfile(ctx, sqlcgen.UpdateUserProfileParams{
		ID:                                 id,
		DisplayName:                        pgconv.TrimPtr(in.DisplayName),
		Bio:                                pgconv.TrimPtr(in.Bio),
		Unlisted:                           in.Unlisted,
		HistoryEnabled:                     in.HistoryEnabled,
		ProfilePublic:                      in.ProfilePublic,
		ShowBluesky:                        in.ShowBluesky,
		SearchHistoryEnabled:               in.SearchHistoryEnabled,
		PersonalizedSearchEnabled:          in.PersonalizedSearchEnabled,
		PersonalizedRecommendationsEnabled: in.PersonalizedRecommendationsEnabled,
		SetSensitiveContentPolicy:          setSensitive,
		SensitiveContentPolicy:             sensitiveVal,
	})
	if err != nil {
		return sqlcgen.User{}, ErrAccountNotFound
	}
	return user, nil
}
