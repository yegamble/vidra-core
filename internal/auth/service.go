package auth

import (
	"context"
	"errors"
	"strings"

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
)

// Repository is the data access the auth service needs. *sqlcgen.Queries
// satisfies it directly, so the production wiring is a one-liner and tests can
// substitute an in-memory fake.
type Repository interface {
	CreateUser(ctx context.Context, arg sqlcgen.CreateUserParams) (sqlcgen.User, error)
	GetUserByEmail(ctx context.Context, lowerEmail string) (sqlcgen.User, error)
	CountUsers(ctx context.Context) (int64, error)
}

// Service holds the auth application logic.
type Service struct {
	repo   Repository
	issuer *TokenIssuer
}

// NewService builds the auth service.
func NewService(repo Repository, issuer *TokenIssuer) *Service {
	return &Service{repo: repo, issuer: issuer}
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

// Register creates an account and returns it with a freshly issued access token.
// The very first account on a fresh instance is granted the admin role
// (bootstrap owner); all others default to "user". Username/email uniqueness is
// enforced by the database; a violation maps to ErrConflict.
func (s *Service) Register(ctx context.Context, in RegisterInput) (sqlcgen.User, string, error) {
	hash, err := HashPassword(in.Password)
	if err != nil {
		return sqlcgen.User{}, "", err
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
			return sqlcgen.User{}, "", ErrConflict
		}
		return sqlcgen.User{}, "", err
	}

	token, err := s.issuer.Issue(user.ID, user.Role)
	if err != nil {
		return sqlcgen.User{}, "", err
	}
	return user, token, nil
}

// Login verifies credentials and returns the account with an access token.
// Unknown account and wrong password are indistinguishable (ErrInvalidCredentials).
func (s *Service) Login(ctx context.Context, in LoginInput) (sqlcgen.User, string, error) {
	user, err := s.repo.GetUserByEmail(ctx, strings.TrimSpace(in.Email))
	if err != nil {
		// Run a dummy compare to keep timing roughly constant whether or not the
		// account exists, reducing user-enumeration via response time.
		_ = CheckPassword("$2a$12$0000000000000000000000000000000000000000000000000000", in.Password)
		return sqlcgen.User{}, "", ErrInvalidCredentials
	}
	if err := CheckPassword(user.PasswordHash, in.Password); err != nil {
		return sqlcgen.User{}, "", ErrInvalidCredentials
	}
	if !user.IsActive {
		return sqlcgen.User{}, "", ErrAccountDisabled
	}

	token, err := s.issuer.Issue(user.ID, user.Role)
	if err != nil {
		return sqlcgen.User{}, "", err
	}
	return user, token, nil
}

// isUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
