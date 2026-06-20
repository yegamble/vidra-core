package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

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
)

// Repository is the data access the auth service needs. *sqlcgen.Queries
// satisfies it directly, so the production wiring is a one-liner and tests can
// substitute an in-memory fake.
type Repository interface {
	CreateUser(ctx context.Context, arg sqlcgen.CreateUserParams) (sqlcgen.User, error)
	GetUserByEmail(ctx context.Context, lowerEmail string) (sqlcgen.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error)
	CountUsers(ctx context.Context) (int64, error)
	UpdateUserProfile(ctx context.Context, arg sqlcgen.UpdateUserProfileParams) (sqlcgen.User, error)

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

// LoginInput is validated login data.
type LoginInput struct {
	Email    string
	Password string
}

// Register creates an account and returns it with a fresh access + refresh token
// pair. The very first account on a fresh instance is granted the admin role
// (bootstrap owner); all others default to "user". Username/email uniqueness is
// enforced by the database; a violation maps to ErrConflict.
func (s *Service) Register(ctx context.Context, in RegisterInput, userAgent string) (sqlcgen.User, Tokens, error) {
	hash, err := HashPassword(in.Password)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, err
	}

	role := "user"
	if n, err := s.repo.CountUsers(ctx); err == nil && n == 0 {
		role = "admin"
	}

	user, err := s.repo.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username:     strings.TrimSpace(in.Username),
		Email:        strings.TrimSpace(in.Email),
		PasswordHash: hash,
		Role:         role,
	})
	if err != nil {
		if isUniqueViolation(err) {
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

// Login verifies credentials and returns the account with an access + refresh
// token pair. Unknown account and wrong password are indistinguishable
// (ErrInvalidCredentials).
func (s *Service) Login(ctx context.Context, in LoginInput, userAgent string) (sqlcgen.User, Tokens, error) {
	user, err := s.repo.GetUserByEmail(ctx, strings.TrimSpace(in.Email))
	if err != nil {
		// Run a dummy compare to keep timing roughly constant whether or not the
		// account exists, reducing user-enumeration via response time.
		_ = CheckPassword("$2a$12$0000000000000000000000000000000000000000000000000000", in.Password)
		return sqlcgen.User{}, Tokens{}, ErrInvalidCredentials
	}
	if err := CheckPassword(user.PasswordHash, in.Password); err != nil {
		return sqlcgen.User{}, Tokens{}, ErrInvalidCredentials
	}
	if !user.IsActive {
		return sqlcgen.User{}, Tokens{}, ErrAccountDisabled
	}

	tokens, err := s.issueTokens(ctx, user, userAgent)
	if err != nil {
		return sqlcgen.User{}, Tokens{}, err
	}
	return user, tokens, nil
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

// ProfileInput is a partial account-profile update: nil fields are unchanged.
type ProfileInput struct {
	DisplayName *string
	Bio         *string
}

// UpdateProfile updates the authenticated account's presentation fields
// (display name, bio). Identity fields (username, email) are intentionally not
// changed here — those need their own re-verification flow.
func (s *Service) UpdateProfile(ctx context.Context, id uuid.UUID, in ProfileInput) (sqlcgen.User, error) {
	user, err := s.repo.UpdateUserProfile(ctx, sqlcgen.UpdateUserProfileParams{
		ID:          id,
		DisplayName: trimPtr(in.DisplayName),
		Bio:         trimPtr(in.Bio),
	})
	if err != nil {
		return sqlcgen.User{}, ErrAccountNotFound
	}
	return user, nil
}

// trimPtr trims a non-nil string pointer's value, leaving nil untouched so a
// COALESCE update skips the column.
func trimPtr(p *string) *string {
	if p == nil {
		return nil
	}
	t := strings.TrimSpace(*p)
	return &t
}

// isUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
