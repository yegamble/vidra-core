package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestDeactivateAccountDisablesAndRevokes(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)
	u, tokens := register(t, svc, "ada", "ada@example.test")

	if err := svc.DeactivateAccount(context.Background(), u.ID, "supersecret"); err != nil {
		t.Fatalf("DeactivateAccount: %v", err)
	}

	// The account is now inactive: UserByID treats it as not found.
	if _, err := svc.UserByID(context.Background(), u.ID); err != ErrAccountNotFound {
		t.Errorf("UserByID after deactivate = %v, want ErrAccountNotFound", err)
	}
	// Sessions are revoked: the registration refresh token no longer rotates.
	if _, _, err := svc.Refresh(context.Background(), tokens.RefreshToken, "test-agent"); err != ErrInvalidRefresh {
		t.Errorf("refresh after deactivate = %v, want ErrInvalidRefresh", err)
	}
}

func TestDeactivateAccountWrongPassword(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo)
	u, _ := register(t, svc, "ada", "ada@example.test")

	if err := svc.DeactivateAccount(context.Background(), u.ID, "not-the-password"); err != ErrInvalidPassword {
		t.Fatalf("DeactivateAccount wrong password = %v, want ErrInvalidPassword", err)
	}
	// The account must remain active after a failed attempt.
	if _, err := svc.UserByID(context.Background(), u.ID); err != nil {
		t.Errorf("account should still be active after a failed deactivate: %v", err)
	}
}

func TestDeactivateAccountUnknownUser(t *testing.T) {
	svc := newTestService(newFakeRepo())
	if err := svc.DeactivateAccount(context.Background(), uuid.New(), "whatever"); err != ErrAccountNotFound {
		t.Errorf("unknown user = %v, want ErrAccountNotFound", err)
	}
}
