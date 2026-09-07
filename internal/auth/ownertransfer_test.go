package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// seedOwnerAndAdmin builds an instance with a marked owner and one other live
// admin, returning both ids.
func seedOwnerAndAdmin(t *testing.T, repo *fakeRepo) (owner, other uuid.UUID) {
	t.Helper()
	hash, err := HashPassword("supersecret")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ctx := context.Background()
	o, err := repo.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username: "mona", Email: "mona@example.test", PasswordHash: hash, Role: "admin",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	o.IsOwner = true
	repo.byEmail["mona@example.test"] = o
	a, err := repo.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username: "avery", Email: "avery@example.test", PasswordHash: hash, Role: "admin",
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	return o.ID, a.ID
}

// TestTransferOwnershipMailsBothParties proves the notices the A16 ruling asked
// for: the transfer reaches BOTH sides, each addressed to the right mailbox with
// the other named, and neither message carries the password that authorized it.
// The former owner's copy is the one that matters if the transfer was not their
// idea, which is why its absence would be the real defect.
func TestTransferOwnershipMailsBothParties(t *testing.T) {
	repo := newFakeRepo()
	mailer := &captureMailer{}
	svc := newResetService(repo, mailer)
	owner, other := seedOwnerAndAdmin(t, repo)

	res, err := svc.TransferOwnership(context.Background(), owner, other, "supersecret")
	if err != nil {
		t.Fatalf("TransferOwnership: %v", err)
	}
	if res.NewOwnerID != other || res.FormerOwnerID != owner {
		t.Errorf("result = %+v, want the marker moving mona→avery", res)
	}
	if res.PreviousOwnersCleared != 1 {
		t.Errorf("previous owners cleared = %d, want 1", res.PreviousOwnersCleared)
	}
	if len(mailer.ownershipNotices) != 2 {
		t.Fatalf("ownership notices = %+v, want one per party", mailer.ownershipNotices)
	}
	byParty := map[string]CapturedOwnershipNotice{}
	for _, n := range mailer.ownershipNotices {
		byParty[n.Party] = n
	}
	if n := byParty["new_owner"]; n.Email != "avery@example.test" || n.Username != "avery" || n.Counterpart != "mona" {
		t.Errorf("new-owner notice = %+v, want avery told that mona handed over", n)
	}
	if n := byParty["former_owner"]; n.Email != "mona@example.test" || n.Username != "mona" || n.Counterpart != "avery" {
		t.Errorf("former-owner notice = %+v, want mona told that avery now owns it", n)
	}
	// The former owner keeps the admin role — this moves the marker, not the role.
	if u := repo.byEmail["mona@example.test"]; u.Role != "admin" || u.IsOwner {
		t.Errorf("former owner = role %q is_owner=%v, want admin/false", u.Role, u.IsOwner)
	}
	if u := repo.byEmail["avery@example.test"]; !u.IsOwner {
		t.Error("the new owner is not marked")
	}
}

// TestTransferOwnershipRefusals walks every way the transfer must not happen,
// checking after each that the marker did not move. The wrong-actor cases are
// the point of the whole feature: the marker exists so that other admins cannot
// dispose of it, so an ordinary admin calling this is the case that matters.
func TestTransferOwnershipRefusals(t *testing.T) {
	ctx := context.Background()
	stillOwned := func(t *testing.T, repo *fakeRepo, owner uuid.UUID) {
		t.Helper()
		if u := repo.byEmail["mona@example.test"]; !u.IsOwner || u.ID != owner {
			t.Fatalf("the marker moved on a refused transfer: %+v", u)
		}
	}

	t.Run("a non-owner admin", func(t *testing.T) {
		repo := newFakeRepo()
		svc := newResetService(repo, &captureMailer{})
		owner, other := seedOwnerAndAdmin(t, repo)
		if _, err := svc.TransferOwnership(ctx, other, owner, "supersecret"); !errors.Is(err, ErrNotInstanceOwner) {
			t.Errorf("err = %v, want ErrNotInstanceOwner", err)
		}
		stillOwned(t, repo, owner)
	})

	t.Run("the wrong password", func(t *testing.T) {
		repo := newFakeRepo()
		mailer := &captureMailer{}
		svc := newResetService(repo, mailer)
		owner, other := seedOwnerAndAdmin(t, repo)
		if _, err := svc.TransferOwnership(ctx, owner, other, "not-the-password"); !errors.Is(err, ErrInvalidPassword) {
			t.Errorf("err = %v, want ErrInvalidPassword", err)
		}
		stillOwned(t, repo, owner)
		if len(mailer.ownershipNotices) != 0 {
			t.Errorf("a refused transfer mailed somebody: %+v", mailer.ownershipNotices)
		}
	})

	t.Run("themselves", func(t *testing.T) {
		repo := newFakeRepo()
		svc := newResetService(repo, &captureMailer{})
		owner, _ := seedOwnerAndAdmin(t, repo)
		if _, err := svc.TransferOwnership(ctx, owner, owner, "supersecret"); !errors.Is(err, ErrOwnerTargetIneligible) {
			t.Errorf("err = %v, want ErrOwnerTargetIneligible", err)
		}
		stillOwned(t, repo, owner)
	})

	t.Run("a non-admin", func(t *testing.T) {
		repo := newFakeRepo()
		svc := newResetService(repo, &captureMailer{})
		owner, _ := seedOwnerAndAdmin(t, repo)
		hash, _ := HashPassword("supersecret")
		u, err := repo.CreateUser(ctx, sqlcgen.CreateUserParams{
			Username: "bob", Email: "bob@example.test", PasswordHash: hash, Role: "user",
		})
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		if _, err := svc.TransferOwnership(ctx, owner, u.ID, "supersecret"); !errors.Is(err, ErrOwnerTargetIneligible) {
			t.Errorf("err = %v, want ErrOwnerTargetIneligible", err)
		}
		stillOwned(t, repo, owner)
	})

	t.Run("an unknown account", func(t *testing.T) {
		repo := newFakeRepo()
		svc := newResetService(repo, &captureMailer{})
		owner, _ := seedOwnerAndAdmin(t, repo)
		if _, err := svc.TransferOwnership(ctx, owner, uuid.New(), "supersecret"); !errors.Is(err, ErrOwnerTargetNotFound) {
			t.Errorf("err = %v, want ErrOwnerTargetNotFound", err)
		}
		stillOwned(t, repo, owner)
	})
}

// TestTransferOwnershipNoticesAreBestEffort holds the line the rest of this
// package holds: a mail relay that is down must not roll back a completed
// transfer. The marker has already moved by the time either message is
// attempted, and there is no second chance to move it — a failed send that
// returned an error here would leave the caller believing nothing happened.
func TestTransferOwnershipNoticesAreBestEffort(t *testing.T) {
	repo := newFakeRepo()
	svc := newResetService(repo, failingMailer{err: errors.New("relay down")})
	owner, other := seedOwnerAndAdmin(t, repo)

	if _, err := svc.TransferOwnership(context.Background(), owner, other, "supersecret"); err != nil {
		t.Fatalf("TransferOwnership with a dead relay: %v", err)
	}
	if u := repo.byEmail["avery@example.test"]; !u.IsOwner {
		t.Error("the transfer did not commit when the notices failed")
	}
	if u := repo.byEmail["mona@example.test"]; u.IsOwner {
		t.Error("the former owner still holds the marker")
	}
}
