package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The step-up capability exists to close A30's dead end: an account created by
// ATProto or OIDC sign-in is passwordless AND carries an unroutable
// …@atproto.invalid address, so it could satisfy neither of the two proofs
// every sensitive self-service action wants, and the unlink refusal's own
// remedy ("set a password first") was unreachable.
//
// What these tests hold down is not "the happy path works" — it is the four
// bindings that make the assertion worth accepting at all: it is spendable
// ONCE, only by the SESSION that earned it, only for the ACCOUNT it was issued
// to, and only inside its window.

// passwordlessAccount registers an account and then empties its password hash,
// which is exactly the shape the ATProto/OIDC creation paths write (bcrypt can
// never verify an empty hash). It returns the account and a session id to bind
// step-ups to.
func passwordlessAccount(t *testing.T, svc *Service, repo *fakeRepo, name, email string) (sqlcgen.User, uuid.UUID) {
	t.Helper()
	user, _ := register(t, svc, name, email)
	if err := repo.UpdateUserPassword(context.Background(), sqlcgen.UpdateUserPasswordParams{ID: user.ID}); err != nil {
		t.Fatalf("UpdateUserPassword: %v", err)
	}
	user.PasswordHash = ""
	user.Email = email
	return user, uuid.New()
}

// grant runs a step-up mint and returns the raw token.
func grant(t *testing.T, svc *Service, userID, sessionID uuid.UUID) string {
	t.Helper()
	g, err := svc.IssueStepUp(context.Background(), userID, sessionID, "atproto")
	if err != nil {
		t.Fatalf("IssueStepUp: %v", err)
	}
	if g.Token == "" {
		t.Fatal("IssueStepUp returned an empty token")
	}
	return g.Token
}

func TestSetPasswordFromStepUp(t *testing.T) {
	repo := newFakeRepo()
	mailer := &captureMailer{}
	svc := newChangeService(repo, mailer)
	user, session := passwordlessAccount(t, svc, repo, "alice", "did-plc-alice@atproto.invalid")

	// Without an assertion there is no way in — this is the whole point: a
	// stolen access token alone must not be able to mint a credential.
	if err := svc.SetPassword(context.Background(), user.ID, "a-brand-new-password", "", session.String()); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("SetPassword with no assertion = %v, want ErrStepUpRequired", err)
	}

	token := grant(t, svc, user.ID, session)
	if err := svc.SetPassword(context.Background(), user.ID, "a-brand-new-password", token, session.String()); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	// Read the credential back out of the repository rather than trusting the
	// return: the account must now be able to sign in with the password.
	after, err := repo.GetUserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if after.PasswordHash == "" {
		t.Fatal("SetPassword left the stored hash empty")
	}
	if err := CheckPassword(after.PasswordHash, "a-brand-new-password"); err != nil {
		t.Fatalf("the stored hash does not verify the password that was set: %v", err)
	}

	// The notice is ATTEMPTED, and it goes to the account's address — which
	// here is the unroutable placeholder. The service does not decide whether
	// it can be delivered; what matters is that nothing else claims it was.
	if len(mailer.changed) != 1 || mailer.changed[0] != "did-plc-alice@atproto.invalid" {
		t.Errorf("password-changed notice = %v, want one attempt to the account address", mailer.changed)
	}

	// Single use: the same assertion cannot set a second password.
	if err := svc.SetPassword(context.Background(), user.ID, "another-password-x", token, session.String()); !errors.Is(err, ErrPasswordAlreadySet) {
		// The account now HAS a password, so the routing answer wins before the
		// token is even looked at. Prove the token is dead too, on a fresh
		// password-less account below.
		t.Fatalf("re-set with a spent token = %v, want ErrPasswordAlreadySet", err)
	}
}

func TestStepUpTokenIsSingleUse(t *testing.T) {
	repo := newFakeRepo()
	svc := newChangeService(repo, &captureMailer{})
	user, session := passwordlessAccount(t, svc, repo, "alice", "did-plc-alice@atproto.invalid")

	token := grant(t, svc, user.ID, session)
	if _, err := svc.ConsumeStepUp(context.Background(), user.ID, session.String(), token); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if _, err := svc.ConsumeStepUp(context.Background(), user.ID, session.String(), token); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("second consume = %v, want ErrStepUpRequired", err)
	}
}

func TestStepUpTokenBindings(t *testing.T) {
	repo := newFakeRepo()
	svc := newChangeService(repo, &captureMailer{})
	alice, aliceSession := passwordlessAccount(t, svc, repo, "alice", "did-plc-alice@atproto.invalid")
	bob, _ := passwordlessAccount(t, svc, repo, "bob", "did-plc-bob@atproto.invalid")

	t.Run("another session cannot spend it", func(t *testing.T) {
		token := grant(t, svc, alice.ID, aliceSession)
		other := uuid.New()
		if _, err := svc.ConsumeStepUp(context.Background(), alice.ID, other.String(), token); !errors.Is(err, ErrStepUpRequired) {
			t.Fatalf("other session = %v, want ErrStepUpRequired", err)
		}
		// And the assertion survives for the session that DID earn it: a failed
		// theft must not burn the legitimate holder's proof.
		if _, err := svc.ConsumeStepUp(context.Background(), alice.ID, aliceSession.String(), token); err != nil {
			t.Fatalf("owning session after a failed theft: %v", err)
		}
	})

	t.Run("another account cannot spend it", func(t *testing.T) {
		token := grant(t, svc, alice.ID, aliceSession)
		if _, err := svc.ConsumeStepUp(context.Background(), bob.ID, aliceSession.String(), token); !errors.Is(err, ErrStepUpRequired) {
			t.Fatalf("other account = %v, want ErrStepUpRequired", err)
		}
	})

	t.Run("a caller with no session binding cannot spend it", func(t *testing.T) {
		token := grant(t, svc, alice.ID, aliceSession)
		if _, err := svc.ConsumeStepUp(context.Background(), alice.ID, "", token); !errors.Is(err, ErrStepUpRequired) {
			t.Fatalf("unbound caller = %v, want ErrStepUpRequired", err)
		}
	})

	t.Run("an expired assertion is dead", func(t *testing.T) {
		token := grant(t, svc, alice.ID, aliceSession)
		// Age it past the window the way the database would: the row's own
		// expires_at is the authority, so moving it is the honest simulation.
		for _, r := range repo.stepUps {
			r.ExpiresAt = time.Now().Add(-time.Second)
		}
		if _, err := svc.ConsumeStepUp(context.Background(), alice.ID, aliceSession.String(), token); !errors.Is(err, ErrStepUpRequired) {
			t.Fatalf("expired assertion = %v, want ErrStepUpRequired", err)
		}
	})

	t.Run("a second challenge supersedes the first", func(t *testing.T) {
		first := grant(t, svc, alice.ID, aliceSession)
		second := grant(t, svc, alice.ID, aliceSession)
		if first == second {
			t.Fatal("the second challenge reused the first token")
		}
		if _, err := svc.ConsumeStepUp(context.Background(), alice.ID, aliceSession.String(), first); !errors.Is(err, ErrStepUpRequired) {
			t.Fatalf("superseded assertion = %v, want it dead", err)
		}
		if _, err := svc.ConsumeStepUp(context.Background(), alice.ID, aliceSession.String(), second); err != nil {
			t.Fatalf("the live assertion: %v", err)
		}
	})
}

// TestSetPasswordRefusesAccountThatHasOne pins the routing rule that keeps a
// step-up from becoming a way AROUND a password: an account with a hash belongs
// on ChangePassword, which re-verifies it. The assertion must survive the
// refusal — burning it would make the user redo the whole provider round trip
// to be told the same thing.
func TestSetPasswordRefusesAccountThatHasOne(t *testing.T) {
	repo := newFakeRepo()
	svc := newChangeService(repo, &captureMailer{})
	user, _ := register(t, svc, "ada", "ada@example.test")
	session := uuid.New()
	token := grant(t, svc, user.ID, session)

	if err := svc.SetPassword(context.Background(), user.ID, "a-different-password", token, session.String()); !errors.Is(err, ErrPasswordAlreadySet) {
		t.Fatalf("SetPassword on a password account = %v, want ErrPasswordAlreadySet", err)
	}
	// The original password still works: nothing was rotated.
	after, err := repo.GetUserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPassword(after.PasswordHash, "supersecret"); err != nil {
		t.Fatalf("the refusal changed the password anyway: %v", err)
	}
	// The assertion was not spent.
	if _, err := svc.ConsumeStepUp(context.Background(), user.ID, session.String(), token); err != nil {
		t.Fatalf("the refusal burned the assertion: %v", err)
	}
}

// TestSetPasswordRevokesOtherSessions: the consequence has to be the password
// change's, exactly. Access tokens are session-bound, so revoking the other
// sessions is what actually signs the other devices out.
func TestSetPasswordRevokesOtherSessions(t *testing.T) {
	repo := newFakeRepo()
	svc := newChangeService(repo, &captureMailer{})
	user, _ := register(t, svc, "alice", "did-plc-alice@atproto.invalid")
	if err := repo.UpdateUserPassword(context.Background(), sqlcgen.UpdateUserPasswordParams{ID: user.ID}); err != nil {
		t.Fatal(err)
	}
	// Two live sessions: the browser doing the set, and another device.
	keep, err := svc.issueTokens(context.Background(), user, "this-browser")
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.issueTokens(context.Background(), user, "other-device")
	if err != nil {
		t.Fatal(err)
	}
	keepSession := sessionIDOfRefresh(t, repo, keep.RefreshToken)

	token := grant(t, svc, user.ID, keepSession)
	if err := svc.SetPassword(context.Background(), user.ID, "a-brand-new-password", token, keepSession.String()); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	if _, _, err := svc.Refresh(context.Background(), other.RefreshToken, "other-device"); err == nil {
		t.Error("the other device's session survived the password being set")
	}
	if _, _, err := svc.Refresh(context.Background(), keep.RefreshToken, "this-browser"); err != nil {
		t.Errorf("the browser that set the password was signed out too: %v", err)
	}
}

// sessionIDOfRefresh finds the session a refresh token belongs to, so a test
// can name "the caller's own session" the way the HTTP layer does.
func sessionIDOfRefresh(t *testing.T, repo *fakeRepo, refresh string) uuid.UUID {
	t.Helper()
	row, err := repo.GetSessionByRefreshHash(context.Background(), hashRefreshToken(refresh))
	if err != nil {
		t.Fatalf("GetSessionByRefreshHash: %v", err)
	}
	return row.ID
}

// --- SC2: a real email from a session ---------------------------------------

func TestRequestEmailChangeWithStepUp(t *testing.T) {
	repo := newFakeRepo()
	mailer := &changeMailer{}
	svc := newChangeService(repo, mailer)
	user, session := passwordlessAccount(t, svc, repo, "alice", "did-plc-alice@atproto.invalid")

	// The shipped password path still refuses this account honestly.
	if _, err := svc.RequestEmailChange(context.Background(), user.ID, "anything", "alice@example.test"); !errors.Is(err, ErrPasswordNotSet) {
		t.Fatalf("password path on a passwordless account = %v, want ErrPasswordNotSet", err)
	}
	// And without an assertion the step-up path refuses too.
	if _, err := svc.RequestEmailChangeWithStepUp(context.Background(), user.ID, session.String(), "", "alice@example.test"); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("no assertion = %v, want ErrStepUpRequired", err)
	}

	token := grant(t, svc, user.ID, session)
	pending, err := svc.RequestEmailChangeWithStepUp(context.Background(), user.ID, session.String(), token, "alice@example.test")
	if err != nil {
		t.Fatalf("RequestEmailChangeWithStepUp: %v", err)
	}
	if pending.NewEmail != "alice@example.test" {
		t.Errorf("pending = %+v, want the requested address", pending)
	}
	// The confirmation went to the NEW address and nowhere else: possession of
	// that mailbox is the proof being collected, and the step-up does not
	// replace it.
	if len(mailer.changeTokens) != 1 || mailer.changeTokens[0].To != "alice@example.test" {
		t.Fatalf("confirmation delivery = %+v, want exactly one, to the new address", mailer.changeTokens)
	}
	// The live address is UNCHANGED until the token is confirmed.
	if got := emailOf(t, repo, user.ID); got != "did-plc-alice@atproto.invalid" {
		t.Errorf("live address moved before confirmation: %q", got)
	}

	if _, newEmail, err := svc.ConfirmEmailChange(context.Background(), user.ID, mailer.lastToken(t)); err != nil || newEmail != "alice@example.test" {
		t.Fatalf("ConfirmEmailChange = %q, %v", newEmail, err)
	}
	if got := emailOf(t, repo, user.ID); got != "alice@example.test" {
		t.Errorf("live address = %q, want the confirmed one", got)
	}
	// No notice to the old address: it was a name in an RFC 2606 reserved
	// domain, so there was no old mailbox to warn and pretending otherwise
	// would be a guaranteed bounce plus a UI that believes a warning landed.
	if len(mailer.notices) != 0 {
		t.Errorf("a notice was sent to the unroutable placeholder: %+v", mailer.notices)
	}
}

// TestConfirmEmailChangeStillNotifiesARealOldAddress is the other half of the
// rule above: the suppression is keyed on the address being undeliverable, not
// on "this account once used a provider".
func TestConfirmEmailChangeStillNotifiesARealOldAddress(t *testing.T) {
	repo := newFakeRepo()
	mailer := &changeMailer{}
	svc := newChangeService(repo, mailer)
	user, _ := register(t, svc, "ada", "ada@example.test")

	if _, err := svc.RequestEmailChange(context.Background(), user.ID, "supersecret", "ada.new@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ConfirmEmailChange(context.Background(), user.ID, mailer.lastToken(t)); err != nil {
		t.Fatal(err)
	}
	if len(mailer.notices) != 1 || mailer.notices[0].Old != "ada@example.test" {
		t.Errorf("notices = %+v, want one to the real old address", mailer.notices)
	}
}

func TestRequestEmailChangeWithStepUpRefusesPasswordAccount(t *testing.T) {
	repo := newFakeRepo()
	mailer := &changeMailer{}
	svc := newChangeService(repo, mailer)
	user, _ := register(t, svc, "ada", "ada@example.test")
	session := uuid.New()
	token := grant(t, svc, user.ID, session)

	if _, err := svc.RequestEmailChangeWithStepUp(context.Background(), user.ID, session.String(), token, "ada.new@example.test"); !errors.Is(err, ErrPasswordAlreadySet) {
		t.Fatalf("step-up path on a password account = %v, want ErrPasswordAlreadySet", err)
	}
	if len(mailer.changeTokens) != 0 {
		t.Errorf("a refused request still mailed a token: %+v", mailer.changeTokens)
	}
}

// TestEmailChangeStepUpKeepsRegistrationRules: the step-up authorises WHO, never
// WHAT. Uniqueness against another account's sign-in identifier — email or
// username, because sign-in accepts both — still applies.
func TestEmailChangeStepUpKeepsRegistrationRules(t *testing.T) {
	repo := newFakeRepo()
	mailer := &changeMailer{}
	svc := newChangeService(repo, mailer)
	_, _ = register(t, svc, "ada", "ada@example.test")
	user, session := passwordlessAccount(t, svc, repo, "alice", "did-plc-alice@atproto.invalid")

	token := grant(t, svc, user.ID, session)
	if _, err := svc.RequestEmailChangeWithStepUp(context.Background(), user.ID, session.String(), token, "ada@example.test"); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("taking another account's address = %v, want ErrEmailTaken", err)
	}
	if len(mailer.changeTokens) != 0 {
		t.Errorf("a refused request still mailed a token: %+v", mailer.changeTokens)
	}
}

// --- the placeholder predicate ----------------------------------------------

func TestIsPlaceholderEmail(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{"did-plc-nmrfcsxnecn7fb27gg5rnv2j@atproto.invalid", true},
		{"someone@ATPROTO.INVALID", true},
		{"someone@sub.example.invalid", true},
		{"someone@example.invalid.", true}, // a trailing root dot is still the same domain
		{"ada@example.test", false},
		{"ada@invalid.example", false}, // ".invalid" as a LABEL, not the TLD
		{"not-an-address", false},
		{"", false},
	} {
		if got := IsPlaceholderEmail(tc.addr); got != tc.want {
			t.Errorf("IsPlaceholderEmail(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

// TestStepUpProvidersFor: the 403 has to name a remedy, and the remedy is the
// caller's own linked identities.
func TestStepUpProvidersFor(t *testing.T) {
	repo := newFakeRepo()
	svc := newChangeService(repo, &captureMailer{})
	user, _ := passwordlessAccount(t, svc, repo, "alice", "did-plc-alice@atproto.invalid")

	if got := svc.StepUpProvidersFor(context.Background(), user.ID); len(got) != 0 {
		t.Errorf("providers with nothing linked = %v, want none", got)
	}
	if svc.HasLinkedProvider(context.Background(), user.ID, "atproto") {
		t.Error("HasLinkedProvider said yes with nothing linked")
	}
	repo.linkIdentity(user.ID, "atproto", "did:plc:alice")
	got := svc.StepUpProvidersFor(context.Background(), user.ID)
	if strings.Join(got, ",") != "atproto" {
		t.Errorf("providers = %v, want [atproto]", got)
	}
	if !svc.HasLinkedProvider(context.Background(), user.ID, "atproto") {
		t.Error("HasLinkedProvider said no for a linked provider")
	}
	if svc.HasLinkedProvider(context.Background(), user.ID, "google") {
		t.Error("HasLinkedProvider said yes for a provider that is not linked")
	}
}

// used to keep the pgtype import honest in the fake's UsedAt handling.
var _ = pgtype.Timestamptz{}

// TestStepUpSurvivesTheRotationTheRedirectCauses is the test the lab wrote.
//
// A step-up can only ever complete as a TOP-LEVEL redirect back from the
// provider — that is the whole transport. A top-level navigation discards the
// in-memory access token, so the landing page redeems a new one from the
// refresh cookie, and `Refresh` ROTATES: it revokes the session row and creates
// a new one with a new id. The assertion is bound to (user, session), so the
// binding it was minted against is destroyed by the very page load that
// receives it, and the form it unlocks then answers 403 step_up_required —
// measured in a real browser, every time, with the token's own row sitting
// unspent in step_up_tokens.
//
// The binding still means what it says. A rotation is not a different browser:
// it required the previous refresh token, which only this browser held, and
// a token lifted out of somebody else's redirect still has no session here to
// spend against. So the assertion moves with the rotation rather than dying on
// it.
func TestStepUpSurvivesTheRotationTheRedirectCauses(t *testing.T) {
	repo := newFakeRepo()
	svc := newChangeService(repo, &captureMailer{})
	user, _ := register(t, svc, "alice", "did-plc-alice@atproto.invalid")
	if err := repo.UpdateUserPassword(context.Background(), sqlcgen.UpdateUserPasswordParams{ID: user.ID}); err != nil {
		t.Fatal(err)
	}
	browser, err := svc.issueTokens(context.Background(), user, "this-browser")
	if err != nil {
		t.Fatal(err)
	}
	started := sessionIDOfRefresh(t, repo, browser.RefreshToken)
	token := grant(t, svc, user.ID, started)

	// The landing page load: one ordinary refresh, exactly what the browser
	// does with the redirect it was just handed.
	_, rotated, err := svc.Refresh(context.Background(), browser.RefreshToken, "this-browser")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	landed := sessionIDOfRefresh(t, repo, rotated.RefreshToken)
	if landed == started {
		t.Fatal("Refresh did not rotate the session — this test would prove nothing")
	}

	if err := svc.SetPassword(context.Background(), user.ID, "a-brand-new-password", token, landed.String()); err != nil {
		t.Fatalf("the assertion did not survive the rotation the redirect causes: %v", err)
	}
	// And it is still single-use afterwards: carrying it forward must not
	// resurrect it for a second spend.
	if _, err := svc.ConsumeStepUp(context.Background(), user.ID, landed.String(), token); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("the carried assertion spent twice = %v, want ErrStepUpRequired", err)
	}
}

// TestStepUpDoesNotFollowAnotherBrowsersRotation holds the other half down: the
// carry is keyed on the rotating session, so one browser refreshing must never
// hand a second browser's assertion to itself.
func TestStepUpDoesNotFollowAnotherBrowsersRotation(t *testing.T) {
	repo := newFakeRepo()
	svc := newChangeService(repo, &captureMailer{})
	user, _ := register(t, svc, "alice", "did-plc-alice@atproto.invalid")
	if err := repo.UpdateUserPassword(context.Background(), sqlcgen.UpdateUserPasswordParams{ID: user.ID}); err != nil {
		t.Fatal(err)
	}
	earner, err := svc.issueTokens(context.Background(), user, "the-browser-that-earned-it")
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.issueTokens(context.Background(), user, "another-browser")
	if err != nil {
		t.Fatal(err)
	}
	token := grant(t, svc, user.ID, sessionIDOfRefresh(t, repo, earner.RefreshToken))

	_, rotated, err := svc.Refresh(context.Background(), other.RefreshToken, "another-browser")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	otherLanded := sessionIDOfRefresh(t, repo, rotated.RefreshToken)
	if _, err := svc.ConsumeStepUp(context.Background(), user.ID, otherLanded.String(), token); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("another browser's rotation collected the assertion = %v, want ErrStepUpRequired", err)
	}
}
