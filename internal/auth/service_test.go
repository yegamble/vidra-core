package auth

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRegReq is an in-memory registration request row.
type fakeRegReq struct {
	id            uuid.UUID
	username      string
	email         string
	passwordHash  string
	note          string
	status        string
	moderatorNote string
	reviewedAt    pgtype.Timestamptz
	createdAt     time.Time
}

// fakeRepo is an in-memory auth.Repository keyed by lowercased email/username.
type fakeRepo struct {
	byEmail  map[string]sqlcgen.User
	names    map[string]bool
	sessions map[uuid.UUID]*sqlcgen.GetSessionByRefreshHashRow
	resets   map[string]*sqlcgen.PasswordResetToken     // keyed by token hash
	verifs   map[string]*sqlcgen.EmailVerificationToken // keyed by token hash
	regReqs  []*fakeRegReq
	// ownerClaim mirrors the single-row owner_claim_tokens table (0104). Nil =
	// never minted, so most tests register freely, exactly like a database
	// that predates the owner-claim flow.
	ownerClaim *sqlcgen.OwnerClaimToken
	// Lookup counters let tests prove Login performs EXACTLY ONE account
	// lookup (and therefore exactly one password compare) per attempt — a
	// second lookup after a failed compare would be a fallthrough that lets
	// one identifier reach two accounts.
	loginLookups int
	emailLookups int
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

// reset zeroes the lookup counters so a test can measure one specific call.
func (f *fakeRepo) reset() {
	f.loginLookups = 0
	f.emailLookups = 0
}

func (f *fakeRepo) CreateRegistrationRequest(_ context.Context, a sqlcgen.CreateRegistrationRequestParams) (sqlcgen.CreateRegistrationRequestRow, error) {
	for _, r := range f.regReqs {
		if r.status == "pending" && (lower(r.email) == lower(a.Email) || lower(r.username) == lower(a.Username)) {
			return sqlcgen.CreateRegistrationRequestRow{}, &pgconn.PgError{Code: "23505"}
		}
	}
	rr := &fakeRegReq{
		id: uuid.New(), username: a.Username, email: a.Email, passwordHash: a.PasswordHash,
		note: a.Note, status: "pending", createdAt: time.Now(),
	}
	f.regReqs = append(f.regReqs, rr)
	return sqlcgen.CreateRegistrationRequestRow{
		ID: rr.id, Username: rr.username, Email: rr.email, Note: rr.note,
		Status: rr.status, ModeratorNote: rr.moderatorNote, CreatedAt: rr.createdAt,
	}, nil
}

func (f *fakeRepo) ListRegistrationRequests(_ context.Context, a sqlcgen.ListRegistrationRequestsParams) ([]sqlcgen.ListRegistrationRequestsRow, error) {
	var rows []sqlcgen.ListRegistrationRequestsRow
	for i := len(f.regReqs) - 1; i >= 0; i-- { // newest first
		r := f.regReqs[i]
		if a.Status != nil && r.status != *a.Status {
			continue
		}
		rows = append(rows, sqlcgen.ListRegistrationRequestsRow{
			ID: r.id, Username: r.username, Email: r.email, Note: r.note,
			Status: r.status, ModeratorNote: r.moderatorNote, ReviewedAt: r.reviewedAt, CreatedAt: r.createdAt,
		})
	}
	return rows, nil
}

func (f *fakeRepo) ApproveRegistrationRequest(ctx context.Context, a sqlcgen.ApproveRegistrationRequestParams) (sqlcgen.ApproveRegistrationRequestRow, error) {
	for _, r := range f.regReqs {
		if r.id == a.ID && r.status == "pending" {
			u, err := f.CreateUser(ctx, sqlcgen.CreateUserParams{
				Username: r.username, Email: r.email, PasswordHash: r.passwordHash, Role: "user",
				PendingEmailVerification: a.PendingEmailVerification,
				HistoryEnabled:           a.HistoryEnabled,
			})
			if err != nil {
				return sqlcgen.ApproveRegistrationRequestRow{}, err // e.g. 23505 conflict
			}
			r.status = "approved"
			r.reviewedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			return sqlcgen.ApproveRegistrationRequestRow{
				ID: u.ID, Username: u.Username, Email: u.Email, PasswordHash: u.PasswordHash,
				Role: u.Role, EmailVerified: u.EmailVerified, IsActive: u.IsActive,
				CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, DisplayName: u.DisplayName, Bio: u.Bio,
				PendingEmailVerification: u.PendingEmailVerification,
			}, nil
		}
	}
	return sqlcgen.ApproveRegistrationRequestRow{}, pgx.ErrNoRows
}

func (f *fakeRepo) RejectRegistrationRequest(_ context.Context, a sqlcgen.RejectRegistrationRequestParams) (int64, error) {
	for _, r := range f.regReqs {
		if r.id == a.ID && r.status == "pending" {
			r.status = "rejected"
			r.moderatorNote = a.ModeratorNote
			r.reviewedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			return 1, nil
		}
	}
	return 0, nil
}

func (f *fakeRepo) UpsertOwnerClaimToken(_ context.Context, tokenHash string) (sqlcgen.OwnerClaimToken, error) {
	f.ownerClaim = &sqlcgen.OwnerClaimToken{ID: true, TokenHash: tokenHash, CreatedAt: time.Now()}
	return *f.ownerClaim, nil
}

func (f *fakeRepo) GetUnclaimedOwnerClaimToken(context.Context) (sqlcgen.OwnerClaimToken, error) {
	if f.ownerClaim == nil || f.ownerClaim.ClaimedAt.Valid {
		return sqlcgen.OwnerClaimToken{}, pgx.ErrNoRows
	}
	return *f.ownerClaim, nil
}

// ClaimOwnerAndCreateAdmin mirrors the single-statement CTE: the
// claimed_at-IS-NULL guard is the single-winner gate (loser gets ErrNoRows)
// and a unique violation leaves the token unclaimed.
func (f *fakeRepo) ClaimOwnerAndCreateAdmin(ctx context.Context, a sqlcgen.ClaimOwnerAndCreateAdminParams) (sqlcgen.ClaimOwnerAndCreateAdminRow, error) {
	if f.ownerClaim == nil || f.ownerClaim.TokenHash != a.TokenHash || f.ownerClaim.ClaimedAt.Valid {
		return sqlcgen.ClaimOwnerAndCreateAdminRow{}, pgx.ErrNoRows
	}
	u, err := f.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username: a.Username, Email: a.Email, PasswordHash: a.PasswordHash,
		Role: "admin", HistoryEnabled: a.HistoryEnabled,
	})
	if err != nil {
		return sqlcgen.ClaimOwnerAndCreateAdminRow{}, err
	}
	f.ownerClaim.ClaimedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	return sqlcgen.ClaimOwnerAndCreateAdminRow{
		ID: u.ID, Username: u.Username, Email: u.Email, PasswordHash: u.PasswordHash,
		Role: u.Role, EmailVerified: u.EmailVerified, IsActive: u.IsActive,
		CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, DisplayName: u.DisplayName,
		Bio: u.Bio, PendingEmailVerification: u.PendingEmailVerification,
		HistoryEnabled: u.HistoryEnabled,
	}, nil
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

// RevokeOtherUserSessions mirrors the SQL: every session for the user EXCEPT
// the named one.
func (f *fakeRepo) RevokeOtherUserSessions(_ context.Context, a sqlcgen.RevokeOtherUserSessionsParams) error {
	for _, s := range f.sessions {
		if s.UserID == a.UserID && s.ID != a.ID {
			s.RevokedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}
	}
	return nil
}

// GetActiveSessionForAccessToken mirrors the SQL join: no row for a revoked or
// expired session, or for a disabled/tombstoned account.
func (f *fakeRepo) GetActiveSessionForAccessToken(_ context.Context, id uuid.UUID) (sqlcgen.GetActiveSessionForAccessTokenRow, error) {
	s, ok := f.sessions[id]
	if !ok || s.RevokedAt.Valid || !s.ExpiresAt.After(time.Now()) {
		return sqlcgen.GetActiveSessionForAccessTokenRow{}, pgx.ErrNoRows
	}
	for _, u := range f.byEmail {
		if u.ID == s.UserID {
			if !u.IsActive || u.DeletedAt.Valid {
				return sqlcgen.GetActiveSessionForAccessTokenRow{}, pgx.ErrNoRows
			}
			return sqlcgen.GetActiveSessionForAccessTokenRow{ID: s.ID, UserID: s.UserID}, nil
		}
	}
	return sqlcgen.GetActiveSessionForAccessTokenRow{}, pgx.ErrNoRows
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
			if a.HistoryEnabled != nil {
				u.HistoryEnabled = *a.HistoryEnabled
			}
			if a.ProfilePublic != nil {
				u.ProfilePublic = *a.ProfilePublic
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

		PendingEmailVerification: arg.PendingEmailVerification,
		HistoryEnabled:           arg.HistoryEnabled,
	}
	f.byEmail[email] = u
	f.names[lower(arg.Username)] = true
	return u, nil
}

func (f *fakeRepo) GetUserByEmail(_ context.Context, lowerEmail string) (sqlcgen.User, error) {
	f.emailLookups++
	u, ok := f.byEmail[lower(lowerEmail)]
	if !ok {
		return sqlcgen.User{}, errors.New("not found")
	}
	return u, nil
}

// GetUserByLoginIdentifier mirrors the real query exactly: the email branch is
// tried first (email always wins over a lookalike username), the username
// branch second, and NEITHER filters on is_active.
func (f *fakeRepo) GetUserByLoginIdentifier(_ context.Context, identifier string) (sqlcgen.User, error) {
	f.loginLookups++
	if u, ok := f.byEmail[lower(identifier)]; ok {
		return u, nil
	}
	for _, u := range f.byEmail {
		if lower(u.Username) == lower(identifier) {
			return u, nil
		}
	}
	return sqlcgen.User{}, errors.New("not found")
}

func (f *fakeRepo) GetUserByID(_ context.Context, id uuid.UUID) (sqlcgen.User, error) {
	for _, u := range f.byEmail {
		if u.ID == id {
			return u, nil
		}
	}
	return sqlcgen.User{}, errors.New("not found")
}

func (f *fakeRepo) GetPublicUserProfileByUsername(_ context.Context, username string) (sqlcgen.GetPublicUserProfileByUsernameRow, error) {
	for _, u := range f.byEmail {
		if lower(u.Username) == lower(username) && u.IsActive && u.ProfilePublic {
			return sqlcgen.GetPublicUserProfileByUsernameRow{
				ID: u.ID, Username: u.Username, DisplayName: u.DisplayName,
				Bio: u.Bio, CreatedAt: u.CreatedAt, ProfilePublic: true,
			}, nil
		}
	}
	return sqlcgen.GetPublicUserProfileByUsernameRow{}, errors.New("not found")
}

// SearchPublicAccounts mirrors the real query's gate exactly:
// GetPublicUserProfileByUsername's active+profile_public rule, plus the
// discovery opt-out a result list has to honour and a direct profile lookup
// does not.
func (f *fakeRepo) SearchPublicAccounts(_ context.Context, a sqlcgen.SearchPublicAccountsParams) ([]sqlcgen.SearchPublicAccountsRow, error) {
	q := lower(a.Query)
	var out []sqlcgen.SearchPublicAccountsRow
	for _, u := range f.byEmail {
		if !u.IsActive || !u.ProfilePublic || u.Unlisted {
			continue
		}
		if !strings.Contains(lower(u.Username), q) && !strings.Contains(lower(u.DisplayName), q) {
			continue
		}
		out = append(out, sqlcgen.SearchPublicAccountsRow{
			ID: u.ID, Username: u.Username, DisplayName: u.DisplayName,
			Bio: u.Bio, CreatedAt: u.CreatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	lo := min(int(a.ResultOffset), len(out))
	out = out[lo:]
	if a.ResultLimit > 0 && int(a.ResultLimit) < len(out) {
		out = out[:a.ResultLimit]
	}
	return out, nil
}

func (f *fakeRepo) CountSearchPublicAccounts(ctx context.Context, a sqlcgen.CountSearchPublicAccountsParams) (int64, error) {
	rows, err := f.SearchPublicAccounts(ctx, sqlcgen.SearchPublicAccountsParams{
		Query: a.Query, ViewerID: a.ViewerID, ResultLimit: 1 << 30,
	})
	return int64(len(rows)), err
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

func TestRegisterFirstUserIsPlainUser(t *testing.T) {
	// Registration never mints admins (0104): even the very first registered
	// account is a plain user — the admin exists only via ClaimOwner.
	user, tok := register(t, newTestService(newFakeRepo()), "ada", "ada@example.test")
	if user.Role != "user" {
		t.Errorf("first user role = %q, want user", user.Role)
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

	res, err := svc.Login(context.Background(), LoginInput{Email: "ADA@example.test", Password: "supersecret"}, "test-agent")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if res.Tokens.AccessToken == "" || res.Tokens.RefreshToken == "" || res.User.Username != "ada" {
		t.Errorf("unexpected login result: %+v", res)
	}
	if res.MFARequired || res.MFAToken != "" {
		t.Errorf("no-MFA login must not require a challenge: %+v", res)
	}
}

func TestLoginWrongPasswordIsInvalidCredentials(t *testing.T) {
	svc := newTestService(newFakeRepo())
	register(t, svc, "ada", "ada@example.test")

	if _, err := svc.Login(context.Background(), LoginInput{Email: "ada@example.test", Password: "nope"}, "a"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestLoginUnknownAccountIsInvalidCredentials(t *testing.T) {
	svc := newTestService(newFakeRepo())
	if _, err := svc.Login(context.Background(), LoginInput{Email: "ghost@example.test", Password: "whatever"}, "a"); !errors.Is(err, ErrInvalidCredentials) {
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

func (f *fakeRepo) CountRegistrationRequests(ctx context.Context, status *string) (int64, error) {
	rows, err := f.ListRegistrationRequests(ctx, sqlcgen.ListRegistrationRequestsParams{Status: status, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}
