package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// seedRecoveryOwner registers an account, marks it owner, and returns it with
// its live session so revocation is observable.
func seedRecoveryOwner(t *testing.T, svc *Service, repo *fakeRepo) (sqlcgen.User, uuid.UUID) {
	t.Helper()
	u, _ := register(t, svc, "mona", "mona@example.test")
	u.IsOwner = true
	repo.byEmail["mona@example.test"] = u
	for id, s := range repo.sessions {
		if s.UserID == u.ID {
			return u, id
		}
	}
	t.Fatal("registration left no session to observe")
	return u, uuid.Nil
}

func TestIssueOwnerRecoveryRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("no owner", func(t *testing.T) {
		svc, _, _ := newMFAService(t, nil)
		register(t, svc, "ada", "ada@example.test") // a user, not the owner
		if _, err := svc.IssueOwnerRecovery(ctx, false); !errors.Is(err, ErrNoInstanceOwner) {
			t.Fatalf("err = %v, want ErrNoInstanceOwner", err)
		}
	})
	t.Run("disabled owner", func(t *testing.T) {
		svc, repo, _ := newMFAService(t, nil)
		u, _ := seedRecoveryOwner(t, svc, repo)
		u.IsActive = false
		repo.byEmail["mona@example.test"] = u
		if _, err := svc.IssueOwnerRecovery(ctx, false); !errors.Is(err, ErrOwnerAccountDisabled) {
			t.Fatalf("err = %v, want ErrOwnerAccountDisabled", err)
		}
		if len(repo.resets) != 0 {
			t.Error("a token was minted for an account that cannot sign in")
		}
	})
	// A token printed beside "MFA removed" that was not removed would be a false
	// assurance, so the refusal must come before the mint.
	t.Run("remove-mfa with MFA unwired", func(t *testing.T) {
		svc := newTestService(newFakeRepo())
		repo := svc.repo.(*fakeRepo)
		seedRecoveryOwner(t, svc, repo)
		if _, err := svc.IssueOwnerRecovery(ctx, true); !errors.Is(err, ErrMFAUnavailable) {
			t.Fatalf("err = %v, want ErrMFAUnavailable", err)
		}
		if len(repo.resets) != 0 {
			t.Error("a token was minted before the refusal")
		}
	})
}

// The token must be spendable through the SAME ResetPassword the web form
// calls, single-use, expiring after resetTTL, with no mail and no session
// revocation until it is spent.
func TestIssueOwnerRecoveryTokenIsAcceptedByResetPassword(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	mailer := &captureMailer{}
	svc := NewService(repo, newTestIssuer(), time.Hour, WithMailer(mailer), WithMFA(newFakeMFARepo(), nil, "T"))
	u, sid := seedRecoveryOwner(t, svc, repo)

	rec, err := svc.IssueOwnerRecovery(ctx, false)
	if err != nil {
		t.Fatalf("IssueOwnerRecovery: %v", err)
	}
	if rec.Token == "" || rec.OwnerID != u.ID || rec.MFARemoved {
		t.Fatalf("recovery = %+v, want a token for %s with MFARemoved=false", rec, u.ID)
	}
	if d := rec.ExpiresAt.Sub(svc.now().Add(svc.resetTTL)); d > time.Minute || d < -time.Minute {
		t.Errorf("ExpiresAt = %v, want about now+resetTTL", rec.ExpiresAt)
	}
	if mailer.calls != 0 || len(mailer.twoFactorRemoved) != 0 {
		t.Errorf("mailer was used (%d resets, %d notices); this path must not need mail", mailer.calls, len(mailer.twoFactorRemoved))
	}
	if repo.sessions[sid].RevokedAt.Valid {
		t.Error("sessions were revoked before the link was spent")
	}
	if err := svc.ResetPassword(ctx, rec.Token, "a-brand-new-password"); err != nil {
		t.Fatalf("ResetPassword rejected the recovery token: %v", err)
	}
	if err := svc.ResetPassword(ctx, rec.Token, "another-password-1"); !errors.Is(err, ErrInvalidResetToken) {
		t.Errorf("second use err = %v, want ErrInvalidResetToken", err)
	}
}

func TestIssueOwnerRecoveryInvalidatesEarlierTokens(t *testing.T) {
	ctx := context.Background()
	svc, repo, _ := newMFAService(t, nil)
	seedRecoveryOwner(t, svc, repo)
	first, _ := svc.IssueOwnerRecovery(ctx, false)
	second, err := svc.IssueOwnerRecovery(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ResetPassword(ctx, first.Token, "a-brand-new-password"); !errors.Is(err, ErrInvalidResetToken) {
		t.Errorf("superseded token err = %v, want ErrInvalidResetToken", err)
	}
	if err := svc.ResetPassword(ctx, second.Token, "a-brand-new-password"); err != nil {
		t.Errorf("newest token rejected: %v", err)
	}
}

func TestIssueOwnerRecoveryRemoveMFA(t *testing.T) {
	for _, tc := range []struct {
		name       string
		enrolled   bool
		wantResult bool
	}{
		{"with MFA: factor and codes deleted, sessions revoked", true, true},
		{"without MFA: not an error, nothing revoked", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, mfaRepo := newMFAService(t, nil)
			u, sid := seedRecoveryOwner(t, svc, repo)
			if tc.enrolled {
				mfaRepo.rows[u.ID] = &sqlcgen.UserMfa{UserID: u.ID, Enabled: true}
				mfaRepo.codes[u.ID] = []*sqlcgen.MfaRecoveryCode{{UserID: u.ID}}
			}
			rec, err := svc.IssueOwnerRecovery(context.Background(), true)
			if err != nil || rec.Token == "" {
				t.Fatalf("IssueOwnerRecovery = %+v, %v; want a token", rec, err)
			}
			if rec.MFARemoved != tc.wantResult {
				t.Errorf("MFARemoved = %v, want %v", rec.MFARemoved, tc.wantResult)
			}
			if _, ok := mfaRepo.rows[u.ID]; ok || len(mfaRepo.codes[u.ID]) != 0 {
				t.Error("the TOTP row or recovery codes survived")
			}
			if repo.sessions[sid].RevokedAt.Valid != tc.wantResult {
				t.Errorf("session revoked = %v, want %v", repo.sessions[sid].RevokedAt.Valid, tc.wantResult)
			}
		})
	}
}
