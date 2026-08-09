package auth

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// oauthFakeRepo layers in-memory oauth identities over the auth fakeRepo so
// one store backs both the OAuth service and the core service issuing sessions.
type oauthFakeRepo struct {
	*fakeRepo
	identities map[string]sqlcgen.OauthIdentity // keyed provider+"\x00"+subject
}

func newOAuthFakeRepo() *oauthFakeRepo {
	return &oauthFakeRepo{fakeRepo: newFakeRepo(), identities: map[string]sqlcgen.OauthIdentity{}}
}

func identKey(provider, subject string) string { return provider + "\x00" + subject }

func (f *oauthFakeRepo) GetOAuthIdentity(_ context.Context, a sqlcgen.GetOAuthIdentityParams) (sqlcgen.OauthIdentity, error) {
	if id, ok := f.identities[identKey(a.Provider, a.Subject)]; ok {
		return id, nil
	}
	return sqlcgen.OauthIdentity{}, pgx.ErrNoRows
}

func (f *oauthFakeRepo) CreateOAuthIdentity(_ context.Context, a sqlcgen.CreateOAuthIdentityParams) (sqlcgen.OauthIdentity, error) {
	if _, ok := f.identities[identKey(a.Provider, a.Subject)]; ok {
		return sqlcgen.OauthIdentity{}, &pgconn.PgError{Code: "23505"}
	}
	for _, id := range f.identities {
		if id.UserID == a.UserID && id.Provider == a.Provider {
			return sqlcgen.OauthIdentity{}, &pgconn.PgError{Code: "23505"} // one link per provider per user
		}
	}
	id := sqlcgen.OauthIdentity{
		ID: uuid.New(), Provider: a.Provider, Subject: a.Subject,
		UserID: a.UserID, Email: a.Email, Handle: a.Handle, CreatedAt: time.Now(),
	}
	f.identities[identKey(a.Provider, a.Subject)] = id
	return id, nil
}

func (f *oauthFakeRepo) UpdateOAuthIdentityHandle(_ context.Context, a sqlcgen.UpdateOAuthIdentityHandleParams) error {
	key := identKey(a.Provider, a.Subject)
	if id, ok := f.identities[key]; ok {
		id.Handle = a.Handle
		f.identities[key] = id
	}
	return nil
}

func (f *oauthFakeRepo) ListOAuthIdentitiesByUser(_ context.Context, userID uuid.UUID) ([]sqlcgen.OauthIdentity, error) {
	var out []sqlcgen.OauthIdentity
	for _, id := range f.identities {
		if id.UserID == userID {
			out = append(out, id)
		}
	}
	return out, nil
}

func (f *oauthFakeRepo) CountOAuthIdentitiesByUser(_ context.Context, userID uuid.UUID) (int64, error) {
	var n int64
	for _, id := range f.identities {
		if id.UserID == userID {
			n++
		}
	}
	return n, nil
}

func (f *oauthFakeRepo) DeleteOAuthIdentity(_ context.Context, a sqlcgen.DeleteOAuthIdentityParams) (int64, error) {
	for k, id := range f.identities {
		if id.UserID == a.UserID && id.Provider == a.Provider {
			delete(f.identities, k)
			return 1, nil
		}
	}
	return 0, nil
}

func (f *oauthFakeRepo) UsernameExists(_ context.Context, name string) (bool, error) {
	return f.names[lower(name)], nil
}

// newOAuthTestService wires the OAuth service over a shared fake repo. The
// provider list carries a dummy issuer — resolveIdentity/Unlink tests never
// touch the network (the wire flow is covered in internal/httpapi).
func newOAuthTestService(repo *oauthFakeRepo) *OAuthService {
	issuer := NewTokenIssuer("oauth-test-secret-oauth-test-secret-0", "vidra", "vidra", time.Minute)
	svc := NewService(repo.fakeRepo, issuer, time.Hour)
	return NewOAuthService(repo, svc, []OAuthProvider{
		{Name: "fake", IssuerURL: "https://idp.invalid", ClientID: "id", ClientSecret: "secret"},
	})
}

func TestSanitizeUsername(t *testing.T) {
	cases := map[string]string{
		"Jane Doe":         "jane-doe",
		"jane.doe":         "jane.doe",
		"  Ærøskøbing!!  ": "r-sk-bing",
		"---":              "",
		"J":                "j",
		"ALL_CAPS-99":      "all_caps-99",
	}
	for in, want := range cases {
		if got := sanitizeUsername(in); got != want {
			t.Errorf("sanitizeUsername(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeriveUsernamePrefersClaimsAndDedupes(t *testing.T) {
	repo := newOAuthFakeRepo()
	s := newOAuthTestService(repo)
	ctx := context.Background()

	// preferred_username wins.
	got, err := s.deriveUsername(ctx, "Cool User", "Full Name", "someone@example.com")
	if err != nil || got != "cool-user" {
		t.Fatalf("deriveUsername = %q, %v; want cool-user", got, err)
	}

	// Email local part is the fallback; collisions get a numeric suffix.
	repo.names["someone"] = true
	repo.names["someone2"] = true
	got, err = s.deriveUsername(ctx, "", "", "Someone@example.com")
	if err != nil || got != "someone3" {
		t.Fatalf("deriveUsername = %q, %v; want someone3", got, err)
	}

	// Unusable claims fall back to "user"; short bases are padded by the fallback.
	got, err = s.deriveUsername(ctx, "!!", "", "@nolocal")
	if err != nil || got != "user" {
		t.Fatalf("deriveUsername = %q, %v; want user", got, err)
	}

	// A base longer than 24 runes is truncated to leave suffix room.
	long := "abcdefghijklmnopqrstuvwxyz"
	got, err = s.deriveUsername(ctx, long, "", "")
	if err != nil || got != long[:24] {
		t.Fatalf("deriveUsername = %q, %v; want %q", got, err, long[:24])
	}
}

func TestResolveIdentityLoginLinkCreate(t *testing.T) {
	repo := newOAuthFakeRepo()
	s := newOAuthTestService(repo)
	ctx := context.Background()

	// CREATE: unknown identity, no matching account. First account bootstraps
	// as admin (parity with password registration) and inherits email_verified.
	user, tokens, outcome, err := s.resolveIdentity(ctx, "fake", "sub-1", oidcClaims{
		Email: "new@example.com", EmailVerified: true, Name: "New Person",
	}, "ua")
	if err != nil || outcome != OAuthCreated {
		t.Fatalf("create: outcome=%v err=%v", outcome, err)
	}
	if user.Username != "new-person" || !user.EmailVerified || user.Role != "admin" {
		t.Errorf("created user = %+v; want username new-person, verified, admin", user)
	}
	if user.PasswordHash != "" {
		t.Errorf("oauth-created account must have no password hash")
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Errorf("create must issue a full session")
	}

	// LOGIN: the same identity comes back → same account, no duplicate.
	user2, _, outcome, err := s.resolveIdentity(ctx, "fake", "sub-1", oidcClaims{Email: "new@example.com", EmailVerified: true}, "ua")
	if err != nil || outcome != OAuthLogin || user2.ID != user.ID {
		t.Fatalf("login: outcome=%v err=%v id match=%v", outcome, err, user2.ID == user.ID)
	}

	// LINK: a different identity whose VERIFIED email matches an existing
	// password account links to it.
	existing, err := repo.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username: "existing", Email: "linked@example.com", PasswordHash: "x", Role: "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	user3, _, outcome, err := s.resolveIdentity(ctx, "fake", "sub-2", oidcClaims{Email: "Linked@example.com", EmailVerified: true}, "ua")
	if err != nil || outcome != OAuthLinked || user3.ID != existing.ID {
		t.Fatalf("link: outcome=%v err=%v linked to existing=%v", outcome, err, user3.ID == existing.ID)
	}
	if n, _ := repo.CountOAuthIdentitiesByUser(ctx, existing.ID); n != 1 {
		t.Errorf("identities for existing = %d, want 1", n)
	}

	// CONFLICT: an UNVERIFIED matching email must not link (takeover vector)
	// and cannot create a duplicate → ErrOAuthEmailConflict.
	if _, _, _, err := s.resolveIdentity(ctx, "fake", "sub-3", oidcClaims{Email: "linked@example.com", EmailVerified: false}, "ua"); !errors.Is(err, ErrOAuthEmailConflict) {
		t.Errorf("unverified email match: err = %v, want ErrOAuthEmailConflict", err)
	}

	// EMAIL REQUIRED: a new identity without an email cannot create an account.
	if _, _, _, err := s.resolveIdentity(ctx, "fake", "sub-4", oidcClaims{}, "ua"); !errors.Is(err, ErrOAuthEmailMissing) {
		t.Errorf("no email: err = %v, want ErrOAuthEmailMissing", err)
	}

	// DISABLED: a deactivated linked account cannot log in via OAuth.
	if err := repo.DeactivateUser(ctx, existing.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.resolveIdentity(ctx, "fake", "sub-2", oidcClaims{Email: "linked@example.com", EmailVerified: true}, "ua"); !errors.Is(err, ErrAccountDisabled) {
		t.Errorf("disabled account login: err = %v, want ErrAccountDisabled", err)
	}
}

func TestOAuthUnlinkLastCredentialGuard(t *testing.T) {
	repo := newOAuthFakeRepo()
	s := newOAuthTestService(repo)
	ctx := context.Background()

	// A passwordless (OAuth-created) account with one identity cannot drop it.
	user, _, _, err := s.resolveIdentity(ctx, "fake", "solo", oidcClaims{Email: "solo@example.com", EmailVerified: true}, "ua")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Unlink(ctx, user.ID, "fake"); !errors.Is(err, ErrOAuthLastCredential) {
		t.Fatalf("unlink last credential: err = %v, want ErrOAuthLastCredential", err)
	}

	// With a second identity, unlinking one is fine; the survivor then locks.
	if _, err := repo.CreateOAuthIdentity(ctx, sqlcgen.CreateOAuthIdentityParams{
		Provider: "other", Subject: "solo-other", UserID: user.ID, Email: "solo@example.com",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Unlink(ctx, user.ID, "other"); err != nil {
		t.Fatalf("unlink with two identities: %v", err)
	}
	if err := s.Unlink(ctx, user.ID, "fake"); !errors.Is(err, ErrOAuthLastCredential) {
		t.Fatalf("unlink survivor: err = %v, want ErrOAuthLastCredential", err)
	}

	// An account WITH a password may unlink its only identity.
	pw, err := repo.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username: "haspw", Email: "haspw@example.com", PasswordHash: "hash", Role: "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateOAuthIdentity(ctx, sqlcgen.CreateOAuthIdentityParams{
		Provider: "fake", Subject: "haspw-sub", UserID: pw.ID, Email: pw.Email,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Unlink(ctx, pw.ID, "fake"); err != nil {
		t.Fatalf("unlink with password set: %v", err)
	}
	// Unlinking a provider that is not linked is a not-found.
	if err := s.Unlink(ctx, pw.ID, "fake"); !errors.Is(err, ErrOAuthIdentityNotFound) {
		t.Fatalf("unlink unknown: err = %v, want ErrOAuthIdentityNotFound", err)
	}
}

func TestOAuthProviderNamesAndEnabled(t *testing.T) {
	s := NewOAuthService(nil, nil, []OAuthProvider{
		{Name: "google"}, {Name: "github-oidc"},
	})
	names := s.ProviderNames()
	if len(names) != 2 || names[0] != "google" || names[1] != "github-oidc" {
		t.Errorf("ProviderNames() = %v", names)
	}
	if !s.Enabled("google") || s.Enabled("nope") {
		t.Errorf("Enabled() misreports configuration")
	}
	// Zero-provider service (the always-wired default) answers safely.
	empty := NewOAuthService(nil, nil, nil)
	if got := empty.ProviderNames(); got == nil || len(got) != 0 {
		t.Errorf("empty ProviderNames() = %#v, want empty non-nil", got)
	}
	if empty.Enabled("google") {
		t.Errorf("empty service must not report providers enabled")
	}
	if _, _, err := empty.BeginAuth(context.Background(), "google", "http://x/cb"); !errors.Is(err, ErrUnknownOAuthProvider) {
		t.Errorf("BeginAuth on empty service: err = %v, want ErrUnknownOAuthProvider", err)
	}
}

// TestDeriveUsernameCrowdedNamespace drives the loop past its numeric suffixes
// to prove the random fallback still yields a usable name.
func TestDeriveUsernameCrowdedNamespace(t *testing.T) {
	repo := newOAuthFakeRepo()
	s := newOAuthTestService(repo)
	repo.names["popular"] = true
	for i := 2; i <= 50; i++ {
		repo.names["popular"+strconv.Itoa(i)] = true
	}
	got, err := s.deriveUsername(context.Background(), "popular", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < len("popular-1") || got[:8] != "popular-" {
		t.Fatalf("deriveUsername fallback = %q, want popular-<rand>", got)
	}
}
