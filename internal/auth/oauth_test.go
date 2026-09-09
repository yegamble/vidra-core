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

// ApproveRegistrationRequest mirrors the SQL's `ident` CTE: approving a PROVIDER
// request creates the account AND attaches the identity it applied with, in one
// all-or-nothing step. Without this the fake would approve accounts nobody can
// sign into, and the test that proves the next sign-in is a LOGIN would pass for
// the wrong reason.
func (f *oauthFakeRepo) ApproveRegistrationRequest(ctx context.Context, a sqlcgen.ApproveRegistrationRequestParams) (sqlcgen.ApproveRegistrationRequestRow, error) {
	var pending *fakeRegReq
	for _, r := range f.regReqs {
		if r.id == a.ID && r.status == "pending" {
			pending = r
			break
		}
	}
	row, err := f.fakeRepo.ApproveRegistrationRequest(ctx, a)
	if err != nil || pending == nil || pending.oauthProvider == "" {
		return row, err
	}
	f.identities[identKey(pending.oauthProvider, pending.oauthSubject)] = sqlcgen.OauthIdentity{
		ID: uuid.New(), Provider: pending.oauthProvider, Subject: pending.oauthSubject,
		UserID: row.ID, Email: pending.oauthEmail, Handle: pending.oauthHandle,
	}
	if pending.oauthEmailVerified {
		_ = f.SetUserEmailVerified(ctx, row.ID)
	}
	return row, nil
}

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

// TestOAuthCollisionMatrix is the A05 ruling in one table: a provider-asserted
// email NEVER matches an account, verified or not, and a subject only ever
// matches the account it is linked to.
//
// The row that made this necessary is "second provider, owner's address": A05
// signed in as the instance OWNER through a second configured provider that
// merely asserted the owner's address email_verified, with no local credential
// and no consent. Under the old rule it linked and logged in; here it is a
// refusal, and the account it could not take is a PROVIDER-created one, which
// is the case the old rule made most vulnerable (no password to compare, and an
// address the first provider put there).
func TestOAuthCollisionMatrix(t *testing.T) {
	repo := newOAuthFakeRepo()
	s := newOAuthTestService(repo)
	ctx := context.Background()

	// CREATE: unknown identity, no local account with that address. Always a
	// plain user — the admin exists only via the owner-claim flow (parity with
	// password registration, 0104) — and inherits email_verified.
	sess, err := s.resolveIdentity(ctx, "fake", "sub-1", oidcClaims{
		Email: "new@example.com", EmailVerified: true, Name: "New Person",
	}, "ua")
	if err != nil || sess.Outcome != OAuthCreated {
		t.Fatalf("create: outcome=%v err=%v", sess.Outcome, err)
	}
	created := sess.User
	if created.Username != "new-person" || !created.EmailVerified || created.Role != "user" {
		t.Errorf("created user = %+v; want username new-person, verified, user", created)
	}
	if created.PasswordHash != "" {
		t.Errorf("oauth-created account must have no password hash")
	}
	if sess.Tokens.AccessToken == "" || sess.Tokens.RefreshToken == "" {
		t.Errorf("create must issue a full session")
	}

	// LOGIN BY SUBJECT: the same identity comes back → the same account, no
	// duplicate. This is the ONLY matching rule there is.
	again, err := s.resolveIdentity(ctx, "fake", "sub-1", oidcClaims{Email: "new@example.com", EmailVerified: true}, "ua")
	if err != nil || again.Outcome != OAuthLogin || again.User.ID != created.ID {
		t.Fatalf("login: outcome=%v err=%v id match=%v", again.Outcome, err, again.User.ID == created.ID)
	}

	// A DIFFERENT PROVIDER asserting the SAME address as the provider-created
	// account above is refused. Subject, never email — and the account with no
	// password is exactly the one an email rule could not protect.
	if _, err := s.resolveIdentity(ctx, "other", "other-sub", oidcClaims{
		Email: "new@example.com", EmailVerified: true,
	}, "ua"); !errors.Is(err, ErrOAuthEmailConflict) {
		t.Errorf("second provider claiming a provider account's address: err = %v, want ErrOAuthEmailConflict", err)
	}
	if n, _ := repo.CountOAuthIdentitiesByUser(ctx, created.ID); n != 1 {
		t.Errorf("identities on the provider-created account = %d, want 1 (nothing was attached)", n)
	}

	existing, err := repo.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username: "existing", Email: "linked@example.com", PasswordHash: "x", Role: "user",
	})
	if err != nil {
		t.Fatal(err)
	}

	// A PASSWORD ACCOUNT's address, asserted VERIFIED, is a refusal. This is the
	// case that used to link, and the one the owner takeover rode in on.
	if _, err := s.resolveIdentity(ctx, "fake", "sub-2", oidcClaims{
		Email: "Linked@example.com", EmailVerified: true,
	}, "ua"); !errors.Is(err, ErrOAuthEmailConflict) {
		t.Errorf("verified email match: err = %v, want ErrOAuthEmailConflict", err)
	}
	// …and UNVERIFIED is the same refusal, so the answer cannot be probed for
	// whether the provider verified the address.
	if _, err := s.resolveIdentity(ctx, "fake", "sub-3", oidcClaims{
		Email: "linked@example.com", EmailVerified: false,
	}, "ua"); !errors.Is(err, ErrOAuthEmailConflict) {
		t.Errorf("unverified email match: err = %v, want ErrOAuthEmailConflict", err)
	}
	// NOTHING was written by either refusal: no identity, no session, and the
	// account's own verification state untouched.
	if n, _ := repo.CountOAuthIdentitiesByUser(ctx, existing.ID); n != 0 {
		t.Errorf("identities for the refused account = %d, want 0", n)
	}
	if u, _ := repo.GetUserByID(ctx, existing.ID); u.EmailVerified {
		t.Error("a refused sign-in marked the local address verified")
	}

	// EMAIL REQUIRED: a new identity without an email cannot create an account.
	if _, err := s.resolveIdentity(ctx, "fake", "sub-4", oidcClaims{}, "ua"); !errors.Is(err, ErrOAuthEmailMissing) {
		t.Errorf("no email: err = %v, want ErrOAuthEmailMissing", err)
	}

	// DISABLED: a deactivated LINKED account cannot log in via its provider.
	if err := repo.DeactivateUser(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.resolveIdentity(ctx, "fake", "sub-1", oidcClaims{Email: "new@example.com", EmailVerified: true}, "ua"); !errors.Is(err, ErrAccountDisabled) {
		t.Errorf("disabled account login: err = %v, want ErrAccountDisabled", err)
	}
}

// TestOAuthLinkIdentity covers the flow that replaces the email match: the
// account holder connects a provider from settings, inside a session.
func TestOAuthLinkIdentity(t *testing.T) {
	repo := newOAuthFakeRepo()
	s := newOAuthTestService(repo)
	ctx := context.Background()

	alice, err := repo.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username: "alice", Email: "alice@example.com", PasswordHash: "x", Role: "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := repo.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username: "bob", Email: "bob@example.com", PasswordHash: "x", Role: "user",
	})
	if err != nil {
		t.Fatal(err)
	}

	// HAPPY PATH: a provider address that is the account's OWN, asserted
	// verified, both links and settles the local email_verified fact — the one
	// thing a link legitimately proves, and the one A05 recorded being thrown
	// away.
	as := OAuthAssertion{Provider: "fake", Subject: "alice-sub", Email: "Alice@example.com", EmailVerified: true}
	outcome, err := s.LinkIdentity(ctx, alice.ID, as, nil)
	if err != nil || outcome != OAuthLinked {
		t.Fatalf("link: outcome=%v err=%v", outcome, err)
	}
	if u, _ := repo.GetUserByID(ctx, alice.ID); !u.EmailVerified {
		t.Error("a provider-verified link on the account's OWN address left email_verified false")
	}

	// IDEMPOTENT: the same subject to the same account again is not an error —
	// a second click on Connect is not a mistake worth refusing.
	if outcome, err := s.LinkIdentity(ctx, alice.ID, as, nil); err != nil || outcome != OAuthLogin {
		t.Fatalf("re-link same subject: outcome=%v err=%v", outcome, err)
	}

	// CLAIMED: bob cannot take a subject linked to alice. Refused, and alice
	// keeps it.
	if _, err := s.LinkIdentity(ctx, bob.ID, as, nil); !errors.Is(err, ErrOAuthIdentityClaimed) {
		t.Fatalf("cross-account link: err = %v, want ErrOAuthIdentityClaimed", err)
	}
	ident, err := repo.GetOAuthIdentity(ctx, sqlcgen.GetOAuthIdentityParams{Provider: "fake", Subject: "alice-sub"})
	if err != nil || ident.UserID != alice.ID {
		t.Fatalf("identity moved: owner=%v err=%v", ident.UserID, err)
	}

	// ALREADY LINKED: a SECOND subject at the same provider is refused by the
	// (user_id, provider) unique index, with its own code.
	if _, err := s.LinkIdentity(ctx, alice.ID, OAuthAssertion{
		Provider: "fake", Subject: "alice-other-sub", Email: "alice@example.com",
	}, nil); !errors.Is(err, ErrOAuthProviderAlreadyLinked) {
		t.Fatalf("second subject for a linked provider: err = %v, want ErrOAuthProviderAlreadyLinked", err)
	}

	// A provider address that is NOT the account's own does not touch
	// email_verified, however loudly the provider asserts it.
	if _, err := s.LinkIdentity(ctx, bob.ID, OAuthAssertion{
		Provider: "other", Subject: "bob-sub", Email: "someone.else@example.com", EmailVerified: true,
	}, nil); err != nil {
		t.Fatal(err)
	}
	if u, _ := repo.GetUserByID(ctx, bob.ID); u.EmailVerified {
		t.Error("a verified claim about a DIFFERENT address marked the account's own address verified")
	}

	// The pre-round-trip probes the link START uses.
	if !s.SubjectClaimedByOther(ctx, "fake", "alice-sub", bob.ID) {
		t.Error("SubjectClaimedByOther said no for a subject bob does not own")
	}
	if s.SubjectClaimedByOther(ctx, "fake", "alice-sub", alice.ID) {
		t.Error("SubjectClaimedByOther said yes for the owner")
	}
	if !s.HasProviderLinked(ctx, alice.ID, "fake") || s.HasProviderLinked(ctx, alice.ID, "other") {
		t.Error("HasProviderLinked disagrees with the stored identities")
	}
}

func TestOAuthUnlinkLastCredentialGuard(t *testing.T) {
	repo := newOAuthFakeRepo()
	s := newOAuthTestService(repo)
	ctx := context.Background()

	// A passwordless (OAuth-created) account with one identity cannot drop it.
	soloSess, err := s.resolveIdentity(ctx, "fake", "solo", oidcClaims{Email: "solo@example.com", EmailVerified: true}, "ua")
	if err != nil {
		t.Fatal(err)
	}
	user := soloSess.User
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
