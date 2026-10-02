package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The admin link refuses before it mints: wrong password first (so a caller
// without it learns nothing about which ids exist), then a target that is
// missing, deactivated, the owner or staff. Every refusal leaves the store empty.
func TestAdminIssueResetLinkRefusals(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(newFakeRepo())
	repo := svc.repo.(*fakeRepo)
	admin, _ := register(t, svc, "avery", "avery@example.test")
	plain, _ := register(t, svc, "uma", "uma@example.test")
	set := func(email string, edit func(u *sqlcgen.User)) {
		u := repo.byEmail[email]
		edit(&u)
		repo.byEmail[email] = u
	}
	set("avery@example.test", func(u *sqlcgen.User) { u.Role = "admin" })

	staff, _ := register(t, svc, "milo", "milo@example.test")
	set("milo@example.test", func(u *sqlcgen.User) { u.Role = "moderator" })
	owner, _ := register(t, svc, "mona", "mona@example.test")
	set("mona@example.test", func(u *sqlcgen.User) { u.IsOwner, u.Role = true, "admin" })
	gone, _ := register(t, svc, "gil", "gil@example.test")
	set("gil@example.test", func(u *sqlcgen.User) { u.IsActive = false })

	for name, tc := range map[string]struct {
		pw     string
		target uuid.UUID
		want   error
	}{
		"wrong password": {"nope-nope-nope", plain.ID, ErrInvalidPassword},
		"unknown":        {"supersecret", uuid.New(), ErrAccountNotFound},
		"deactivated":    {"supersecret", gone.ID, ErrResetTargetInactive},
		"owner":          {"supersecret", owner.ID, ErrResetTargetOwner},
		"moderator":      {"supersecret", staff.ID, ErrResetTargetStaff},
		"admin (self)":   {"supersecret", admin.ID, ErrResetTargetStaff},
	} {
		if _, err := svc.AdminIssueResetLink(ctx, admin.ID, tc.pw, tc.target); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	if len(repo.resets) != 0 {
		t.Errorf("refusals minted %d tokens", len(repo.resets))
	}

	// An ordinary target succeeds, expires after resetTTL, and the token spends.
	link, err := svc.AdminIssueResetLink(ctx, admin.ID, "supersecret", plain.ID)
	if err != nil {
		t.Fatalf("AdminIssueResetLink: %v", err)
	}
	if d := time.Until(link.ExpiresAt); d < 59*time.Minute || d > time.Hour {
		t.Errorf("expires in %v, want about 1h", d)
	}
	if err := svc.ResetPassword(ctx, link.Token, "brand-new-pass"); err != nil {
		t.Errorf("ResetPassword with the minted token: %v", err)
	}
}
