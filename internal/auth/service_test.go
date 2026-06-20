package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory auth.Repository keyed by lowercased email/username.
type fakeRepo struct {
	byEmail  map[string]sqlcgen.User
	names    map[string]bool
	sessions map[uuid.UUID]*sqlcgen.GetSessionByRefreshHashRow
	resets   map[string]*sqlcgen.PasswordResetToken     // keyed by token hash
	verifs   map[string]*sqlcgen.EmailVerificationToken // keyed by token hash
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		byEmail:  map[string]sqlcgen.User{},
		names:    map[string]bool{},
		sessions: map[uuid.UUID]*sqlcgen.GetSessionByRefreshHashRow{},
		resets:   map[string]*sqlcgen.PasswordResetToken{},
		verifs:   map[string]*sqlcgen.EmailVerificationToken{},
	}
}

func (f *fakeRepo) CreateEmailVerificationToken(_ context.Context, a sqlcgen.CreateEmailVerificationTokenParams) (sqlcgen.EmailVerificationToken, error) {
	t := sqlcgen.EmailVerificationToken{
		ID: uuid.New(), UserID: a.UserID, TokenHash: a.TokenHash,
		ExpiresAt: a.ExpiresAt, CreatedAt: time.Now(),
	}
	f.verifs[a.TokenHash] = &t
	return t, nil
}

func (f *fakeRepo) GetEmailVerificationToken(_ context.Context, hash string) (sqlcgen.EmailVerificationToken, error) {
	if t, ok := f.verifs[hash]; ok {
		return *t, nil
	}
	return sqlcgen.EmailVerificationToken{}, errors.New("not found")
}

func (f *fakeRepo) MarkEmailVerificationTokenUsed(_ context.Context, id uuid.UUID) error {
	for _, t := range f.verifs {
		if t.ID == id {
			t.UsedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}
	}
	return nil
}

func (f *fakeRepo) DeleteUnusedEmailVerificationTokens(_ context.Context, userID uuid.UUID) error {
	for h, t := range f.verifs {
		if t.UserID == userID && !t.UsedAt.Valid {
			delete(f.verifs, h)
		}
	}
	return nil
}

func (f *fakeRepo) SetUserEmailVerified(_ context.Context, id uuid.UUID) error {
	for k, u := range f.byEmail {
		if u.ID == id {
			u.EmailVerified = true
			u.UpdatedAt = time.Now()
			f.byEmail[k] = u
			return nil
		}
	}
	return errors.New("not found")
}

func (f *fakeRepo) DeactivateUser(_ context.Context, id uuid.UUID) error {
	for k, u := range f.byEmail {
		if u.ID == id {
			u.IsActive = false
			u.UpdatedAt = time.Now()
			f.byEmail[k] = u
			return nil
		}
	}
	return errors.New("not found")
}

func (f *fakeRepo) CreatePasswordResetToken(_ context.Context, a sqlcgen.CreatePasswordResetTokenParams) (sqlcgen.PasswordResetToken, error) {
	t := sqlcgen.PasswordResetToken{
		ID: uuid.New(), UserID: a.UserID, TokenHash: a.TokenHash,
		ExpiresAt: a.ExpiresAt, CreatedAt: time.Now(),
	}
	f.resets[a.TokenHash] = &t
	return t, nil
}

func (f *fakeRepo) GetPasswordResetToken(_ context.Context, hash string) (sqlcgen.PasswordResetToken, error) {
	if t, ok := f.resets[hash]; ok {
		return *t, nil
	}
	return sqlcgen.PasswordResetToken{}, errors.New("not found")
}

func (f *fakeRepo) MarkPasswordResetTokenUsed(_ context.Context, id uuid.UUID) error {
	for _, t := range f.resets {
		if t.ID == id {
			t.UsedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}
	}
	return nil
}

func (f *fakeRepo) DeleteUnusedPasswordResetTokens(_ context.Context, userID uuid.UUID) error {
	for h, t := range f.resets {
		if t.UserID == userID && !t.UsedAt.Valid {
			delete(f.resets, h)
		}
	}
	return nil
}

func (f *fakeRepo) UpdateUserPassword(_ context.Context, a sqlcgen.UpdateUserPasswordParams) error {
	for k, u := range f.byEmail {
		if u.ID == a.ID {
			u.PasswordHash = a.PasswordHash
			u.UpdatedAt = time.Now()
			f.byEmail[k] = u
			return nil
		}
	}
	return errors.New("not found")
}

func (f *fakeRepo) CreateSession(_ context.Context, a sqlcgen.CreateSessionParams) (sqlcgen.CreateSessionRow, error) {
	id := uuid.New()
	f.sessions[id] = &sqlcgen.GetSessionByRefreshHashRow{
		ID: id, UserID: a.UserID, RefreshHash: a.RefreshHash,
		UserAgent: a.UserAgent, ExpiresAt: a.ExpiresAt, CreatedAt: time.Now(),
	}
	return sqlcgen.CreateSessionRow{ID: id, UserID: a.UserID, RefreshHash: a.RefreshHash, ExpiresAt: a.ExpiresAt}, nil
}

func (f *fakeRepo) GetSessionByRefreshHash(_ context.Context, hash string) (sqlcgen.GetSessionByRefreshHashRow, error) {
	for _, s := range f.sessions {
		if s.RefreshHash == hash {
			return *s, nil
		}
	}
	return sqlcgen.GetSessionByRefreshHashRow{}, errors.New("not found")
}

func (f *fakeRepo) RevokeSession(_ context.Context, id uuid.UUID) error {
	if s, ok := f.sessions[id]; ok {
		s.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}
	return nil
}

func (f *fakeRepo) RevokeAllUserSessions(_ context.Context, userID uuid.UUID) error {
	for _, s := range f.sessions {
		if s.UserID == userID {
			s.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}
	}
	return nil
}

func (f *fakeRepo) UpdateUserProfile(_ context.Context, a sqlcgen.UpdateUserProfileParams) (sqlcgen.User, error) {
	for k, u := range f.byEmail {
		if u.ID == a.ID {
			if a.DisplayName != nil {
				u.DisplayName = *a.DisplayName
			}
			if a.Bio != nil {
				u.Bio = *a.Bio
			}
			u.UpdatedAt = time.Now()
			f.byEmail[k] = u
			return u, nil
		}
	}
	return sqlcgen.User{}, errors.New("not found")
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
	return NewService(repo, newTestIssuer(), time.Hour)
}

func register(t *testing.T, svc *Service, name, email string) (sqlcgen.User, Tokens) {
	t.Helper()
	u, tok, err := svc.Register(context.Background(), RegisterInput{Username: name, Email: email, Password: "supersecret"}, "test-agent")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return u, tok
}

func TestRegisterFirstUserIsAdmin(t *testing.T) {
	user, tok := register(t, newTestService(newFakeRepo()), "ada", "ada@example.test")
	if user.Role != "admin" {
		t.Errorf("first user role = %q, want admin", user.Role)
	}
	if tok.AccessToken == "" || tok.RefreshToken == "" {
		t.Error("expected both access and refresh tokens")
	}
}

func TestRegisterSecondUserIsUser(t *testing.T) {
	svc := newTestService(newFakeRepo())
	register(t, svc, "ada", "ada@example.test")
	user, _ := register(t, svc, "bob", "bob@example.test")
	if user.Role != "user" {
		t.Errorf("second user role = %q, want user", user.Role)
	}
}

func TestRegisterDuplicateIsConflict(t *testing.T) {
	svc := newTestService(newFakeRepo())
	register(t, svc, "ada", "ada@example.test")
	_, _, err := svc.Register(context.Background(), RegisterInput{Username: "ada", Email: "ada@example.test", Password: "supersecret"}, "test-agent")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestLoginSuccess(t *testing.T) {
	svc := newTestService(newFakeRepo())
	register(t, svc, "ada", "ada@example.test")

	user, tok, err := svc.Login(context.Background(), LoginInput{Email: "ADA@example.test", Password: "supersecret"}, "test-agent")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tok.AccessToken == "" || tok.RefreshToken == "" || user.Username != "ada" {
		t.Errorf("unexpected login result: user=%+v tokens=%+v", user, tok)
	}
}

func TestLoginWrongPasswordIsInvalidCredentials(t *testing.T) {
	svc := newTestService(newFakeRepo())
	register(t, svc, "ada", "ada@example.test")

	if _, _, err := svc.Login(context.Background(), LoginInput{Email: "ada@example.test", Password: "nope"}, "a"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestLoginUnknownAccountIsInvalidCredentials(t *testing.T) {
	svc := newTestService(newFakeRepo())
	if _, _, err := svc.Login(context.Background(), LoginInput{Email: "ghost@example.test", Password: "whatever"}, "a"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestRefreshRotatesToken(t *testing.T) {
	svc := newTestService(newFakeRepo())
	ctx := context.Background()
	_, tok := register(t, svc, "ada", "ada@example.test")

	_, newTok, err := svc.Refresh(ctx, tok.RefreshToken, "test-agent")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if newTok.RefreshToken == tok.RefreshToken {
		t.Error("refresh token was not rotated")
	}
	if newTok.AccessToken == "" {
		t.Error("expected a new access token")
	}
}

func TestRefreshRejectsUnknownToken(t *testing.T) {
	svc := newTestService(newFakeRepo())
	if _, _, err := svc.Refresh(context.Background(), "not-a-real-refresh-token", "a"); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("err = %v, want ErrInvalidRefresh", err)
	}
}

// TestRefreshReuseRevokesAllSessions verifies rotated-token reuse is treated as
// compromise: the old token is rejected AND the freshly issued one is revoked.
func TestRefreshReuseRevokesAllSessions(t *testing.T) {
	svc := newTestService(newFakeRepo())
	ctx := context.Background()
	_, tok := register(t, svc, "ada", "ada@example.test")

	_, newTok, err := svc.Refresh(ctx, tok.RefreshToken, "a")
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	// Reuse the now-rotated (revoked) original token.
	if _, _, err := svc.Refresh(ctx, tok.RefreshToken, "a"); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("reuse err = %v, want ErrInvalidRefresh", err)
	}
	// The session minted by the first refresh must also be revoked now.
	if _, _, err := svc.Refresh(ctx, newTok.RefreshToken, "a"); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("post-compromise refresh err = %v, want ErrInvalidRefresh", err)
	}
}

func TestLogoutRevokesRefreshToken(t *testing.T) {
	svc := newTestService(newFakeRepo())
	ctx := context.Background()
	_, tok := register(t, svc, "ada", "ada@example.test")

	if err := svc.Logout(ctx, tok.RefreshToken); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, _, err := svc.Refresh(ctx, tok.RefreshToken, "a"); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("refresh after logout err = %v, want ErrInvalidRefresh", err)
	}
}

func TestUpdateProfilePartial(t *testing.T) {
	svc := newTestService(newFakeRepo())
	ctx := context.Background()
	user, _ := register(t, svc, "ada", "ada@example.test")

	bio := "builder"
	updated, err := svc.UpdateProfile(ctx, user.ID, ProfileInput{Bio: &bio})
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if updated.Bio != "builder" {
		t.Errorf("bio = %q, want builder", updated.Bio)
	}
	// display_name left unchanged (nil) — still empty from registration.
	if updated.DisplayName != "" {
		t.Errorf("display_name = %q, want empty (unchanged)", updated.DisplayName)
	}
}

func TestLogoutUnknownTokenIsNoError(t *testing.T) {
	svc := newTestService(newFakeRepo())
	if err := svc.Logout(context.Background(), "unknown"); err != nil {
		t.Fatalf("Logout(unknown) = %v, want nil (idempotent)", err)
	}
}
