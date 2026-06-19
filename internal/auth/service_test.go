package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory auth.Repository keyed by lowercased email/username.
type fakeRepo struct {
	byEmail map[string]sqlcgen.User
	names   map[string]bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{byEmail: map[string]sqlcgen.User{}, names: map[string]bool{}}
}

func (f *fakeRepo) CountUsers(context.Context) (int64, error) {
	return int64(len(f.byEmail)), nil
}

func (f *fakeRepo) CreateUser(_ context.Context, arg sqlcgen.CreateUserParams) (sqlcgen.User, error) {
	email := lower(arg.Email)
	if _, ok := f.byEmail[email]; ok || f.names[lower(arg.Username)] {
		return sqlcgen.User{}, &pgconn.PgError{Code: "23505"}
	}
	u := sqlcgen.User{
		ID:           uuid.New(),
		Username:     arg.Username,
		Email:        arg.Email,
		PasswordHash: arg.PasswordHash,
		Role:         arg.Role,
		IsActive:     true,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	f.byEmail[email] = u
	f.names[lower(arg.Username)] = true
	return u, nil
}

func (f *fakeRepo) GetUserByEmail(_ context.Context, lowerEmail string) (sqlcgen.User, error) {
	u, ok := f.byEmail[lower(lowerEmail)]
	if !ok {
		return sqlcgen.User{}, errors.New("not found")
	}
	return u, nil
}

func (f *fakeRepo) GetUserByID(_ context.Context, id uuid.UUID) (sqlcgen.User, error) {
	for _, u := range f.byEmail {
		if u.ID == id {
			return u, nil
		}
	}
	return sqlcgen.User{}, errors.New("not found")
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func newTestService(repo Repository) *Service {
	return NewService(repo, newTestIssuer())
}

func TestRegisterFirstUserIsAdmin(t *testing.T) {
	svc := newTestService(newFakeRepo())
	user, token, err := svc.Register(context.Background(), RegisterInput{
		Username: "ada", Email: "ada@example.test", Password: "supersecret",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if user.Role != "admin" {
		t.Errorf("first user role = %q, want admin", user.Role)
	}
	if token == "" {
		t.Error("expected a token")
	}
}

func TestRegisterSecondUserIsUser(t *testing.T) {
	svc := newTestService(newFakeRepo())
	ctx := context.Background()
	_, _, _ = svc.Register(ctx, RegisterInput{Username: "ada", Email: "ada@example.test", Password: "supersecret"})
	user, _, err := svc.Register(ctx, RegisterInput{Username: "bob", Email: "bob@example.test", Password: "supersecret"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if user.Role != "user" {
		t.Errorf("second user role = %q, want user", user.Role)
	}
}

func TestRegisterDuplicateIsConflict(t *testing.T) {
	svc := newTestService(newFakeRepo())
	ctx := context.Background()
	_, _, _ = svc.Register(ctx, RegisterInput{Username: "ada", Email: "ada@example.test", Password: "supersecret"})
	_, _, err := svc.Register(ctx, RegisterInput{Username: "ada", Email: "ada@example.test", Password: "supersecret"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestLoginSuccess(t *testing.T) {
	svc := newTestService(newFakeRepo())
	ctx := context.Background()
	_, _, _ = svc.Register(ctx, RegisterInput{Username: "ada", Email: "ada@example.test", Password: "supersecret"})

	user, token, err := svc.Login(ctx, LoginInput{Email: "ADA@example.test", Password: "supersecret"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if token == "" || user.Username != "ada" {
		t.Errorf("unexpected login result: user=%+v token?=%v", user, token != "")
	}
}

func TestLoginWrongPasswordIsInvalidCredentials(t *testing.T) {
	svc := newTestService(newFakeRepo())
	ctx := context.Background()
	_, _, _ = svc.Register(ctx, RegisterInput{Username: "ada", Email: "ada@example.test", Password: "supersecret"})

	if _, _, err := svc.Login(ctx, LoginInput{Email: "ada@example.test", Password: "nope"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestLoginUnknownAccountIsInvalidCredentials(t *testing.T) {
	svc := newTestService(newFakeRepo())
	if _, _, err := svc.Login(context.Background(), LoginInput{Email: "ghost@example.test", Password: "whatever"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
}
