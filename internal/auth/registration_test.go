package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func regInput(name, email string) RegisterInput {
	return RegisterInput{Username: name, Email: email, Password: "supersecret"}
}

func TestRequestRegistrationCreatesPending(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(newFakeRepo())

	req, err := svc.RequestRegistration(ctx, regInput("bob", "bob@example.test"), "please let me in")
	if err != nil {
		t.Fatalf("RequestRegistration: %v", err)
	}
	if req.Status != RegistrationPending || req.Username != "bob" || req.Note != "please let me in" {
		t.Errorf("request = %+v, want pending bob with note", req)
	}

	// A second pending request for the same email/username → conflict.
	if _, err := svc.RequestRegistration(ctx, regInput("bob", "bob@example.test"), ""); err != ErrConflict {
		t.Errorf("duplicate pending err = %v, want ErrConflict", err)
	}

	// It shows in the pending queue.
	list, _, err := svc.ListRegistrationRequests(ctx, "pending", 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != req.ID {
		t.Fatalf("pending queue = %+v, want the one request", list)
	}
}

func TestRequestRegistrationConflictsWithExistingUser(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := newTestService(repo)

	// Register an account the normal way, then a request for the same email fails.
	if _, _, err := svc.Register(ctx, regInput("ada", "ada@example.test"), "ua"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := svc.RequestRegistration(ctx, regInput("ada2", "ada@example.test"), ""); err != ErrConflict {
		t.Errorf("request for existing email err = %v, want ErrConflict", err)
	}
}

func TestApproveRegistrationCreatesAccount(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(newFakeRepo())
	admin := uuid.New()

	req, err := svc.RequestRegistration(ctx, regInput("bob", "bob@example.test"), "")
	if err != nil {
		t.Fatalf("RequestRegistration: %v", err)
	}

	user, err := svc.ApproveRegistration(ctx, admin, req.ID)
	if err != nil {
		t.Fatalf("ApproveRegistration: %v", err)
	}
	if user.Email != "bob@example.test" || user.Role != "user" {
		t.Errorf("approved user = %+v, want bob/user", user)
	}
	// The approved account can now log in.
	if _, err := svc.Login(ctx, LoginInput{Email: "bob@example.test", Password: "supersecret"}, "ua"); err != nil {
		t.Errorf("login after approval: %v", err)
	}
	// Re-approving the same (now resolved) request → not found.
	if _, err := svc.ApproveRegistration(ctx, admin, req.ID); err != ErrRegistrationRequestNotFound {
		t.Errorf("re-approve err = %v, want ErrRegistrationRequestNotFound", err)
	}
	// It is no longer pending.
	if list, _, _ := svc.ListRegistrationRequests(ctx, "pending", 20, 0); len(list) != 0 {
		t.Errorf("pending after approve = %d, want 0", len(list))
	}
}

func TestApproveUnknownRegistration(t *testing.T) {
	svc := newTestService(newFakeRepo())
	if _, err := svc.ApproveRegistration(context.Background(), uuid.New(), uuid.New()); err != ErrRegistrationRequestNotFound {
		t.Errorf("approve unknown err = %v, want ErrRegistrationRequestNotFound", err)
	}
}

func TestRejectRegistration(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(newFakeRepo())
	admin := uuid.New()

	req, err := svc.RequestRegistration(ctx, regInput("carol", "carol@example.test"), "")
	if err != nil {
		t.Fatalf("RequestRegistration: %v", err)
	}
	if err := svc.RejectRegistration(ctx, admin, req.ID, "not now"); err != nil {
		t.Fatalf("RejectRegistration: %v", err)
	}
	// A rejected request cannot be approved.
	if _, err := svc.ApproveRegistration(ctx, admin, req.ID); err != ErrRegistrationRequestNotFound {
		t.Errorf("approve after reject err = %v, want ErrRegistrationRequestNotFound", err)
	}
	// Rejecting again (not pending) → not found.
	if err := svc.RejectRegistration(ctx, admin, req.ID, ""); err != ErrRegistrationRequestNotFound {
		t.Errorf("re-reject err = %v, want ErrRegistrationRequestNotFound", err)
	}
	// No account was created, so login fails.
	if _, err := svc.Login(ctx, LoginInput{Email: "carol@example.test", Password: "supersecret"}, "ua"); err == nil {
		t.Error("login should fail for a rejected registration")
	}
}
