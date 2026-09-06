package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// --- the in-memory half of email_change_requests (migration 0129) -----------
//
// These mirror the SQL exactly, because the properties under test ARE the SQL's:
// ConfirmEmailChange is one statement whose predicate includes used_at IS NULL,
// so a second confirmation matches nothing; and users_email_lower_idx is what
// refuses an address another account has taken since the request. A fake that
// merely "worked" would prove nothing about either.

func (f *fakeRepo) CreateEmailChangeRequest(_ context.Context, a sqlcgen.CreateEmailChangeRequestParams) (sqlcgen.EmailChangeRequest, error) {
	r := sqlcgen.EmailChangeRequest{
		ID: uuid.New(), UserID: a.UserID, NewEmail: a.NewEmail,
		TokenHash: a.TokenHash, ExpiresAt: a.ExpiresAt, CreatedAt: time.Now(),
	}
	if f.emailChanges == nil {
		f.emailChanges = map[string]*sqlcgen.EmailChangeRequest{}
	}
	f.emailChanges[a.TokenHash] = &r
	return r, nil
}

func (f *fakeRepo) GetPendingEmailChangeRequest(_ context.Context, userID uuid.UUID) (sqlcgen.EmailChangeRequest, error) {
	var newest *sqlcgen.EmailChangeRequest
	for _, r := range f.emailChanges {
		if r.UserID != userID || r.UsedAt.Valid || !r.ExpiresAt.After(time.Now()) {
			continue
		}
		if newest == nil || r.CreatedAt.After(newest.CreatedAt) {
			newest = r
		}
	}
	if newest == nil {
		return sqlcgen.EmailChangeRequest{}, pgx.ErrNoRows
	}
	return *newest, nil
}

func (f *fakeRepo) DeleteUnusedEmailChangeRequests(_ context.Context, userID uuid.UUID) (int64, error) {
	var n int64
	for h, r := range f.emailChanges {
		if r.UserID == userID && !r.UsedAt.Valid {
			delete(f.emailChanges, h)
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) ConfirmEmailChange(_ context.Context, a sqlcgen.ConfirmEmailChangeParams) (sqlcgen.ConfirmEmailChangeRow, error) {
	r, ok := f.emailChanges[a.TokenHash]
	// The CTE's full predicate: the right token, the right ACCOUNT, unused, and
	// unexpired. Anything else consumes nothing and updates nothing.
	if !ok || r.UserID != a.UserID || r.UsedAt.Valid || !r.ExpiresAt.After(time.Now()) {
		return sqlcgen.ConfirmEmailChangeRow{}, pgx.ErrNoRows
	}
	// users_email_lower_idx: another account may have taken the address since
	// the request was made.
	if other, ok := f.byEmail[lower(r.NewEmail)]; ok && other.ID != r.UserID {
		return sqlcgen.ConfirmEmailChangeRow{}, &pgconn.PgError{Code: "23505"}
	}
	r.UsedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	for key, u := range f.byEmail {
		if u.ID != r.UserID {
			continue
		}
		delete(f.byEmail, key)
		u.Email = r.NewEmail
		u.EmailVerified = true
		u.UpdatedAt = time.Now()
		f.byEmail[lower(r.NewEmail)] = u
		return sqlcgen.ConfirmEmailChangeRow{ID: u.ID, Email: u.Email}, nil
	}
	return sqlcgen.ConfirmEmailChangeRow{}, pgx.ErrNoRows
}

// changeMailer records what each of the two email-change messages carried, and
// to which address — the addressing IS the security property (the token goes to
// the NEW mailbox, the notice to the OLD one), so it is what the tests assert.
type changeMailer struct {
	captureMailer
	changeTokens []struct{ To, Token string }
	notices      []struct{ Old, New string }
	failVerify   bool
}

func (m *changeMailer) SendEmailChangeVerification(_ context.Context, newEmail, token string) error {
	if m.failVerify {
		return errors.New("mailer down")
	}
	m.changeTokens = append(m.changeTokens, struct{ To, Token string }{newEmail, token})
	return nil
}

func (m *changeMailer) SendEmailChanged(_ context.Context, oldEmail, newEmail string) error {
	m.notices = append(m.notices, struct{ Old, New string }{oldEmail, newEmail})
	return nil
}

func (m *changeMailer) lastToken(t *testing.T) string {
	t.Helper()
	if len(m.changeTokens) == 0 {
		t.Fatal("no email-change token was delivered")
	}
	return m.changeTokens[len(m.changeTokens)-1].Token
}

func newChangeService(repo Repository, mailer Mailer) *Service {
	return NewService(repo, newTestIssuer(), time.Hour, WithMailer(mailer))
}

// emailOf reads the account's CURRENT address back out of the repository, so
// every assertion about "the live address" is an independent readback rather
// than a re-read of what the service returned.
func emailOf(t *testing.T, repo *fakeRepo, id uuid.UUID) string {
	t.Helper()
	u, err := repo.GetUserByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	return u.Email
}

// TestRequestEmailChangeLeavesTheLiveAddressAloneUntilConfirmed is the whole
// point of the two-step shape: asking must change nothing. A one-step PATCH
// would let a stolen access token (plus the password, which this flow does
// re-verify) move the address with no proof the requester can read mail there.
func TestRequestEmailChangeLeavesTheLiveAddressAloneUntilConfirmed(t *testing.T) {
	repo := newFakeRepo()
	mailer := &changeMailer{}
	svc := newChangeService(repo, mailer)
	user, _ := register(t, svc, "ada", "ada@example.test")

	pending, err := svc.RequestEmailChange(context.Background(), user.ID, "supersecret", "ada.new@example.test")
	if err != nil {
		t.Fatalf("RequestEmailChange: %v", err)
	}
	if got := emailOf(t, repo, user.ID); got != "ada@example.test" {
		t.Errorf("live address after the request = %q, want it UNCHANGED", got)
	}
	if pending.NewEmail != "ada.new@example.test" {
		t.Errorf("pending address = %q, want the requested one", pending.NewEmail)
	}
	if !pending.ExpiresAt.After(time.Now()) {
		t.Error("pending request is already expired")
	}
	// The token goes to the NEW address and nowhere else: it is the possession
	// proof, so delivering it to the old mailbox would prove nothing.
	if len(mailer.changeTokens) != 1 || mailer.changeTokens[0].To != "ada.new@example.test" {
		t.Fatalf("confirmation delivered to %+v, want exactly one to the NEW address", mailer.changeTokens)
	}
	if len(mailer.notices) != 0 {
		t.Errorf("a change NOTICE was sent before anything changed: %+v", mailer.notices)
	}
	// The pending state is readable, and carries no token.
	read, err := svc.PendingEmailChangeFor(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("PendingEmailChangeFor: %v", err)
	}
	if read.NewEmail != "ada.new@example.test" {
		t.Errorf("read-back pending address = %q", read.NewEmail)
	}
}

// TestConfirmEmailChangeSwitchesTheAddressAndNotifiesTheOldOne is SC1's second
// half: the token moves the address, marks it verified, clears the pending
// state, and tells the OLD mailbox — the only signal that reaches a user whose
// address was taken from them.
func TestConfirmEmailChangeSwitchesTheAddressAndNotifiesTheOldOne(t *testing.T) {
	repo := newFakeRepo()
	mailer := &changeMailer{}
	svc := newChangeService(repo, mailer)
	user, _ := register(t, svc, "ada", "ada@example.test")

	if _, err := svc.RequestEmailChange(context.Background(), user.ID, "supersecret", "ada.new@example.test"); err != nil {
		t.Fatalf("RequestEmailChange: %v", err)
	}
	token := mailer.lastToken(t)

	oldEmail, newEmail, err := svc.ConfirmEmailChange(context.Background(), user.ID, token)
	if err != nil {
		t.Fatalf("ConfirmEmailChange: %v", err)
	}
	if oldEmail != "ada@example.test" || newEmail != "ada.new@example.test" {
		t.Errorf("confirm returned (%q, %q), want the old then the new address", oldEmail, newEmail)
	}
	u, err := repo.GetUserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if u.Email != "ada.new@example.test" {
		t.Errorf("live address after confirm = %q, want the new one", u.Email)
	}
	if !u.EmailVerified {
		t.Error("email_verified is false after confirming a token delivered to that very address")
	}
	// Pending state is gone.
	if _, err := svc.PendingEmailChangeFor(context.Background(), user.ID); !errors.Is(err, ErrNoPendingEmailChange) {
		t.Errorf("pending state after confirm = %v, want ErrNoPendingEmailChange", err)
	}
	// The notice went to the OLD address and named the new one.
	if len(mailer.notices) != 1 {
		t.Fatalf("notices = %+v, want exactly one", mailer.notices)
	}
	if mailer.notices[0].Old != "ada@example.test" || mailer.notices[0].New != "ada.new@example.test" {
		t.Errorf("notice = %+v, want (old, new)", mailer.notices[0])
	}
	// Single use: replaying the same token changes nothing.
	if _, _, err := svc.ConfirmEmailChange(context.Background(), user.ID, token); !errors.Is(err, ErrInvalidEmailChangeToken) {
		t.Errorf("replaying the token = %v, want ErrInvalidEmailChangeToken", err)
	}
}

// TestSecondEmailChangeRequestSupersedesTheFirstToken: only the newest link may
// work, exactly as the reset flow guarantees. Otherwise a user who mistyped an
// address and asked again would leave a live token for the typo.
func TestSecondEmailChangeRequestSupersedesTheFirstToken(t *testing.T) {
	repo := newFakeRepo()
	mailer := &changeMailer{}
	svc := newChangeService(repo, mailer)
	user, _ := register(t, svc, "ada", "ada@example.test")

	if _, err := svc.RequestEmailChange(context.Background(), user.ID, "supersecret", "typo@example.test"); err != nil {
		t.Fatalf("first request: %v", err)
	}
	first := mailer.lastToken(t)
	if _, err := svc.RequestEmailChange(context.Background(), user.ID, "supersecret", "ada.new@example.test"); err != nil {
		t.Fatalf("second request: %v", err)
	}
	second := mailer.lastToken(t)
	if first == second {
		t.Fatal("the second request reused the first token")
	}
	if _, _, err := svc.ConfirmEmailChange(context.Background(), user.ID, first); !errors.Is(err, ErrInvalidEmailChangeToken) {
		t.Errorf("the superseded token = %v, want ErrInvalidEmailChangeToken", err)
	}
	if got := emailOf(t, repo, user.ID); got != "ada@example.test" {
		t.Fatalf("the dead token moved the address to %q", got)
	}
	if _, _, err := svc.ConfirmEmailChange(context.Background(), user.ID, second); err != nil {
		t.Fatalf("the newest token: %v", err)
	}
	if got := emailOf(t, repo, user.ID); got != "ada.new@example.test" {
		t.Errorf("address after confirming the newest token = %q", got)
	}
}

// TestExpiredEmailChangeTokenIsRefused — the expiry is enforced, and an expired
// request is not "pending" either.
func TestExpiredEmailChangeTokenIsRefused(t *testing.T) {
	repo := newFakeRepo()
	mailer := &changeMailer{}
	svc := newChangeService(repo, mailer)
	user, _ := register(t, svc, "ada", "ada@example.test")

	if _, err := svc.RequestEmailChange(context.Background(), user.ID, "supersecret", "ada.new@example.test"); err != nil {
		t.Fatalf("RequestEmailChange: %v", err)
	}
	token := mailer.lastToken(t)
	// Age the request past its window, the way the clock would.
	for _, r := range repo.emailChanges {
		r.ExpiresAt = time.Now().Add(-time.Minute)
	}
	if _, _, err := svc.ConfirmEmailChange(context.Background(), user.ID, token); !errors.Is(err, ErrInvalidEmailChangeToken) {
		t.Errorf("expired token = %v, want ErrInvalidEmailChangeToken", err)
	}
	if got := emailOf(t, repo, user.ID); got != "ada@example.test" {
		t.Errorf("an expired token moved the address to %q", got)
	}
	if _, err := svc.PendingEmailChangeFor(context.Background(), user.ID); !errors.Is(err, ErrNoPendingEmailChange) {
		t.Errorf("an EXPIRED request still reads as pending: %v", err)
	}
}

// TestConfirmEmailChangeRefusesAnotherAccountsToken: the token proves possession
// of a mailbox, the session proves whose account it is, and BOTH are required.
// A token that leaks out of the one place it sits in plaintext must not move
// anybody's address.
func TestConfirmEmailChangeRefusesAnotherAccountsToken(t *testing.T) {
	repo := newFakeRepo()
	mailer := &changeMailer{}
	svc := newChangeService(repo, mailer)
	ada, _ := register(t, svc, "ada", "ada@example.test")
	bob, _ := register(t, svc, "bob", "bob@example.test")

	if _, err := svc.RequestEmailChange(context.Background(), ada.ID, "supersecret", "ada.new@example.test"); err != nil {
		t.Fatalf("RequestEmailChange: %v", err)
	}
	token := mailer.lastToken(t)

	if _, _, err := svc.ConfirmEmailChange(context.Background(), bob.ID, token); !errors.Is(err, ErrInvalidEmailChangeToken) {
		t.Errorf("bob confirming ada's token = %v, want ErrInvalidEmailChangeToken", err)
	}
	if got := emailOf(t, repo, bob.ID); got != "bob@example.test" {
		t.Errorf("bob's address moved to %q", got)
	}
	if got := emailOf(t, repo, ada.ID); got != "ada@example.test" {
		t.Errorf("ada's address moved to %q", got)
	}
	// And ada's own token still works afterwards: the failed attempt must not
	// have consumed it.
	if _, _, err := svc.ConfirmEmailChange(context.Background(), ada.ID, token); err != nil {
		t.Fatalf("ada's own confirmation after bob's attempt: %v", err)
	}
}

// TestRequestEmailChangeRefusals covers every "nothing changed" rule in one
// place, each asserting the live address afterwards.
func TestRequestEmailChangeRefusals(t *testing.T) {
	repo := newFakeRepo()
	mailer := &changeMailer{}
	svc := newChangeService(repo, mailer)
	ada, _ := register(t, svc, "ada", "ada@example.test")
	register(t, svc, "bob", "bob@example.test")
	// An account whose USERNAME looks like an address. Sign-in resolves email
	// first, so this name can never shadow a real address — but an email equal
	// to it would shadow the NAME, which is the collision this refuses.
	register(t, svc, "carol@example.test", "carol@mail.example.test")

	cases := []struct {
		name     string
		password string
		email    string
		want     error
	}{
		{"wrong current password", "not-the-password", "ada.new@example.test", ErrInvalidPassword},
		{"the address it already has", "supersecret", "ada@example.test", ErrEmailUnchanged},
		{"same address, different case", "supersecret", "ADA@example.test", ErrEmailUnchanged},
		{"another account's address", "supersecret", "bob@example.test", ErrEmailTaken},
		{"another account's USERNAME", "supersecret", "carol@example.test", ErrEmailTaken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.RequestEmailChange(context.Background(), ada.ID, tc.password, tc.email)
			if !errors.Is(err, tc.want) {
				t.Fatalf("RequestEmailChange = %v, want %v", err, tc.want)
			}
			if got := emailOf(t, repo, ada.ID); got != "ada@example.test" {
				t.Errorf("the refused request moved the address to %q", got)
			}
			if _, err := svc.PendingEmailChangeFor(context.Background(), ada.ID); !errors.Is(err, ErrNoPendingEmailChange) {
				t.Errorf("a refused request left something pending: %v", err)
			}
			if len(mailer.changeTokens) != 0 {
				t.Errorf("a refused request still mailed a token: %+v", mailer.changeTokens)
			}
		})
	}
}

// TestRequestEmailChangeRefusesPasswordlessAccount pins the same rule the
// password change ships: an OAuth/ATProto-only account (empty stored hash) is
// told what to do instead of being handed the unfalsifiable "wrong password".
func TestRequestEmailChangeRefusesPasswordlessAccount(t *testing.T) {
	repo := newFakeRepo()
	svc := newChangeService(repo, &changeMailer{})
	user, _ := register(t, svc, "ada", "ada@example.test")
	// The OAuth/ATProto shape: no password bcrypt could ever verify.
	if err := repo.UpdateUserPassword(context.Background(), sqlcgen.UpdateUserPasswordParams{ID: user.ID}); err != nil {
		t.Fatalf("UpdateUserPassword: %v", err)
	}
	if _, err := svc.RequestEmailChange(context.Background(), user.ID, "anything-at-all", "ada.new@example.test"); !errors.Is(err, ErrPasswordNotSet) {
		t.Fatalf("passwordless account = %v, want ErrPasswordNotSet", err)
	}
}

// TestCancelAndResendEmailChange: cancel kills the token, resend replaces it,
// and both refuse honestly when nothing is pending.
func TestCancelAndResendEmailChange(t *testing.T) {
	repo := newFakeRepo()
	mailer := &changeMailer{}
	svc := newChangeService(repo, mailer)
	user, _ := register(t, svc, "ada", "ada@example.test")

	if err := svc.CancelEmailChange(context.Background(), user.ID); !errors.Is(err, ErrNoPendingEmailChange) {
		t.Errorf("cancel with nothing pending = %v, want ErrNoPendingEmailChange", err)
	}
	if _, err := svc.ResendEmailChange(context.Background(), user.ID); !errors.Is(err, ErrNoPendingEmailChange) {
		t.Errorf("resend with nothing pending = %v, want ErrNoPendingEmailChange", err)
	}

	if _, err := svc.RequestEmailChange(context.Background(), user.ID, "supersecret", "ada.new@example.test"); err != nil {
		t.Fatalf("RequestEmailChange: %v", err)
	}
	first := mailer.lastToken(t)
	resent, err := svc.ResendEmailChange(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("ResendEmailChange: %v", err)
	}
	if resent.NewEmail != "ada.new@example.test" {
		t.Errorf("resend addressed %q, want the address already pending", resent.NewEmail)
	}
	second := mailer.lastToken(t)
	if first == second {
		t.Fatal("resend reused the token instead of superseding it")
	}
	if _, _, err := svc.ConfirmEmailChange(context.Background(), user.ID, first); !errors.Is(err, ErrInvalidEmailChangeToken) {
		t.Errorf("the pre-resend token = %v, want it dead", err)
	}

	if err := svc.CancelEmailChange(context.Background(), user.ID); err != nil {
		t.Fatalf("CancelEmailChange: %v", err)
	}
	if _, _, err := svc.ConfirmEmailChange(context.Background(), user.ID, second); !errors.Is(err, ErrInvalidEmailChangeToken) {
		t.Errorf("a cancelled token still confirmed: %v", err)
	}
	if got := emailOf(t, repo, user.ID); got != "ada@example.test" {
		t.Errorf("address after cancel = %q", got)
	}
}

// TestRequestEmailChangeReportsAFailedSend is the deliberate asymmetry: the
// CONFIRMATION is not best-effort (a pending request whose message never left is
// a dead end the user cannot see), while the old-address NOTICE is (the address
// has already moved by then, and reporting a failure would read as "your address
// did not change").
func TestRequestEmailChangeReportsAFailedSend(t *testing.T) {
	repo := newFakeRepo()
	mailer := &changeMailer{failVerify: true}
	svc := newChangeService(repo, mailer)
	user, _ := register(t, svc, "ada", "ada@example.test")

	if _, err := svc.RequestEmailChange(context.Background(), user.ID, "supersecret", "ada.new@example.test"); err == nil {
		t.Fatal("RequestEmailChange returned nil although the message could not be sent")
	}
	if got := emailOf(t, repo, user.ID); got != "ada@example.test" {
		t.Errorf("address moved despite the failed send: %q", got)
	}
}

// TestEmailChangeTokenIsStoredHashedOnly: the raw token exists only in the
// message. A database reader must not be able to complete somebody's change.
func TestEmailChangeTokenIsStoredHashedOnly(t *testing.T) {
	repo := newFakeRepo()
	mailer := &changeMailer{}
	svc := newChangeService(repo, mailer)
	user, _ := register(t, svc, "ada", "ada@example.test")

	if _, err := svc.RequestEmailChange(context.Background(), user.ID, "supersecret", "ada.new@example.test"); err != nil {
		t.Fatalf("RequestEmailChange: %v", err)
	}
	raw := mailer.lastToken(t)
	for hash, r := range repo.emailChanges {
		if strings.Contains(hash, raw) || hash == raw {
			t.Fatal("the RAW token is stored")
		}
		if hash != hashEmailChangeToken(raw) {
			t.Errorf("stored key %q is not the SHA-256 of the delivered token", hash)
		}
		if r.NewEmail != "ada.new@example.test" {
			t.Errorf("stored request address = %q", r.NewEmail)
		}
	}
}
