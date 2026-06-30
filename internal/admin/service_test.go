package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type fakeRepo struct {
	users   map[uuid.UUID]sqlcgen.User
	revoked map[uuid.UUID]bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{users: map[uuid.UUID]sqlcgen.User{}, revoked: map[uuid.UUID]bool{}}
}

func (f *fakeRepo) add(username, role string) uuid.UUID {
	id := uuid.New()
	f.users[id] = sqlcgen.User{
		ID: id, Username: username, Email: username + "@example.test", Role: role,
		IsActive: true, CreatedAt: time.Now(),
	}
	return id
}

func (f *fakeRepo) ListUsers(_ context.Context, a sqlcgen.ListUsersParams) ([]sqlcgen.User, error) {
	var out []sqlcgen.User
	for _, u := range f.users {
		if a.Query == "" || strings.Contains(u.Username, a.Query) || strings.Contains(u.Email, a.Query) {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *fakeRepo) GetUserByID(_ context.Context, id uuid.UUID) (sqlcgen.User, error) {
	u, ok := f.users[id]
	if !ok {
		return sqlcgen.User{}, errors.New("not found")
	}
	return u, nil
}

func (f *fakeRepo) AdminUpdateUser(_ context.Context, a sqlcgen.AdminUpdateUserParams) (sqlcgen.User, error) {
	u := f.users[a.ID]
	if a.Role != nil {
		u.Role = *a.Role
	}
	if a.IsActive != nil {
		u.IsActive = *a.IsActive
	}
	f.users[a.ID] = u
	return u, nil
}

func (f *fakeRepo) RevokeAllUserSessions(_ context.Context, userID uuid.UUID) error {
	f.revoked[userID] = true
	return nil
}

func strptr(s string) *string { return &s }
func boolptr(b bool) *bool    { return &b }

func TestListUsersSearch(t *testing.T) {
	repo := newFakeRepo()
	repo.add("ada", RoleAdmin)
	repo.add("bob", RoleUser)
	svc := NewService(repo)

	all, _ := svc.ListUsers(context.Background(), "", 20, 0)
	if len(all) != 2 {
		t.Fatalf("all users = %d, want 2", len(all))
	}
	only, _ := svc.ListUsers(context.Background(), "bob", 20, 0)
	if len(only) != 1 || only[0].Username != "bob" {
		t.Fatalf("search bob = %+v, want [bob]", only)
	}
}

func TestUpdateUserRoleAndDeactivate(t *testing.T) {
	repo := newFakeRepo()
	admin := repo.add("ada", RoleAdmin)
	bob := repo.add("bob", RoleUser)
	svc := NewService(repo)
	ctx := context.Background()

	// Promote bob to moderator.
	u, err := svc.UpdateUser(ctx, admin, bob, strptr(RoleModerator), nil)
	if err != nil || u.Role != RoleModerator {
		t.Fatalf("promote = (%+v, %v), want moderator", u, err)
	}
	// Deactivate bob → sessions revoked.
	if _, err := svc.UpdateUser(ctx, admin, bob, nil, boolptr(false)); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if !repo.revoked[bob] {
		t.Errorf("deactivating bob did not revoke his sessions")
	}
}

func TestUpdateUserSelfGuardAndNotFound(t *testing.T) {
	repo := newFakeRepo()
	admin := repo.add("ada", RoleAdmin)
	svc := NewService(repo)
	ctx := context.Background()

	// Self-demotion and self-deactivation are rejected.
	if _, err := svc.UpdateUser(ctx, admin, admin, strptr(RoleUser), nil); !errors.Is(err, ErrSelfChange) {
		t.Errorf("self-demote = %v, want ErrSelfChange", err)
	}
	if _, err := svc.UpdateUser(ctx, admin, admin, nil, boolptr(false)); !errors.Is(err, ErrSelfChange) {
		t.Errorf("self-deactivate = %v, want ErrSelfChange", err)
	}
	// Keeping your own role admin is fine (no-op-ish).
	if _, err := svc.UpdateUser(ctx, admin, admin, strptr(RoleAdmin), nil); err != nil {
		t.Errorf("self keep-admin = %v, want nil", err)
	}
	// Unknown target.
	if _, err := svc.UpdateUser(ctx, admin, uuid.New(), strptr(RoleModerator), nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown = %v, want ErrNotFound", err)
	}
}
