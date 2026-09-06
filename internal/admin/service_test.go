package admin

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

type fakeRepo struct {
	users   map[uuid.UUID]sqlcgen.User
	used    map[uuid.UUID]int64
	revoked map[uuid.UUID]bool
	// Instance-wide aggregate stubs for Stats.
	publicVideos int64
	comments     int64
	storedBytes  int64
	peers        int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{users: map[uuid.UUID]sqlcgen.User{}, used: map[uuid.UUID]int64{}, revoked: map[uuid.UUID]bool{}}
}

func (f *fakeRepo) CountUsers(_ context.Context) (int64, error) { return int64(len(f.users)), nil }

// CountUsersMatching mirrors ListUsers' filter so a test asserting the total
// against a filtered page is actually asserting something.
func (f *fakeRepo) CountUsersMatching(_ context.Context, query string) (int64, error) {
	if query == "" {
		return int64(len(f.users)), nil
	}
	q := strings.ToLower(query)
	var n int64
	for _, u := range f.users {
		if strings.Contains(strings.ToLower(u.Username), q) ||
			strings.Contains(strings.ToLower(u.Email), q) {
			n++
		}
	}
	return n, nil
}
func (f *fakeRepo) CountPublicVideos(_ context.Context) (int64, error) {
	return f.publicVideos, nil
}
func (f *fakeRepo) CountComments(_ context.Context) (int64, error)      { return f.comments, nil }
func (f *fakeRepo) SumAllStorageUsage(_ context.Context) (int64, error) { return f.storedBytes, nil }
func (f *fakeRepo) CountFederatedPeers(_ context.Context) (int64, error) {
	return f.peers, nil
}

func (f *fakeRepo) add(username, role string) uuid.UUID {
	id := uuid.New()
	f.users[id] = sqlcgen.User{
		ID: id, Username: username, Email: username + "@example.test", Role: role,
		IsActive: true, CreatedAt: time.Now(),
	}
	return id
}

func (f *fakeRepo) ListUsers(_ context.Context, a sqlcgen.ListUsersParams) ([]sqlcgen.ListUsersRow, error) {
	var out []sqlcgen.ListUsersRow
	for _, u := range f.users {
		if a.Query == "" || strings.Contains(u.Username, a.Query) || strings.Contains(u.Email, a.Query) {
			out = append(out, sqlcgen.ListUsersRow{
				ID: u.ID, Username: u.Username, Email: u.Email, Role: u.Role,
				EmailVerified: u.EmailVerified, IsActive: u.IsActive,
				CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt,
				DisplayName: u.DisplayName, Bio: u.Bio,
				StorageQuotaBytes: u.StorageQuotaBytes, StorageUsedBytes: f.used[u.ID],
			})
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
	if a.EmailVerified != nil {
		u.EmailVerified = *a.EmailVerified
	}
	if a.BypassQuarantine != nil {
		u.BypassQuarantine = *a.BypassQuarantine
	}
	if a.SetStorageQuota {
		u.StorageQuotaBytes = a.StorageQuotaBytes
	}
	f.users[a.ID] = u
	return u, nil
}

func (f *fakeRepo) RevokeAllUserSessions(_ context.Context, userID uuid.UUID) error {
	f.revoked[userID] = true
	return nil
}

func (f *fakeRepo) SumUserStorageUsage(_ context.Context, ownerID uuid.UUID) (int64, error) {
	return f.used[ownerID], nil
}

// CountActiveAdmins mirrors the SQL predicate exactly (role='admin' AND
// is_active AND deleted_at IS NULL). A fake that counted every admin row would
// make the last-admin guard pass its tests while the real query disagreed —
// the fake-fidelity trap this repo has been bitten by twice.
func (f *fakeRepo) CountActiveAdmins(_ context.Context) (int64, error) {
	var n int64
	for _, u := range f.users {
		if u.Role == RoleAdmin && u.IsActive && !u.DeletedAt.Valid {
			n++
		}
	}
	return n, nil
}

// markOwner sets the 0131 owner marker on an existing fake row.
func (f *fakeRepo) markOwner(id uuid.UUID) {
	u := f.users[id]
	u.IsOwner = true
	f.users[id] = u
}

// tombstone mirrors the §1 hard delete's end state: anonymised, deactivated and
// stamped deleted_at.
func (f *fakeRepo) tombstone(id uuid.UUID) {
	u := f.users[id]
	u.IsActive = false
	u.DeletedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	f.users[id] = u
}

func strptr(s string) *string { return &s }
func boolptr(b bool) *bool    { return &b }
func int64ptr(n int64) *int64 { return &n }

func TestListUsersSearch(t *testing.T) {
	repo := newFakeRepo()
	adaID := repo.add("ada", RoleAdmin)
	repo.add("bob", RoleUser)
	repo.used[adaID] = 42
	svc := NewService(repo)

	all, _ := svc.ListUsers(context.Background(), "", 20, 0)
	if len(all) != 2 {
		t.Fatalf("all users = %d, want 2", len(all))
	}
	only, _ := svc.ListUsers(context.Background(), "bob", 20, 0)
	if len(only) != 1 || only[0].Username != "bob" {
		t.Fatalf("search bob = %+v, want [bob]", only)
	}
	// The list rows carry storage usage for the admin view.
	adas, _ := svc.ListUsers(context.Background(), "ada", 20, 0)
	if len(adas) != 1 || adas[0].StorageUsedBytes != 42 {
		t.Errorf("ada storage_used_bytes = %+v, want 42", adas)
	}
}

func TestUpdateUserRoleAndDeactivate(t *testing.T) {
	repo := newFakeRepo()
	admin := repo.add("ada", RoleAdmin)
	bob := repo.add("bob", RoleUser)
	svc := NewService(repo)
	ctx := context.Background()

	// Promote bob to moderator.
	u, err := svc.UpdateUser(ctx, admin, bob, UpdateUserInput{Role: strptr(RoleModerator)})
	if err != nil || u.Role != RoleModerator {
		t.Fatalf("promote = (%+v, %v), want moderator", u, err)
	}
	// Deactivate bob → sessions revoked.
	if _, err := svc.UpdateUser(ctx, admin, bob, UpdateUserInput{IsActive: boolptr(false)}); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if !repo.revoked[bob] {
		t.Errorf("deactivating bob did not revoke his sessions")
	}
}

func TestUpdateUserStorageQuota(t *testing.T) {
	repo := newFakeRepo()
	admin := repo.add("ada", RoleAdmin)
	bob := repo.add("bob", RoleUser)
	svc := NewService(repo)
	ctx := context.Background()

	// Set an override.
	u, err := svc.UpdateUser(ctx, admin, bob, UpdateUserInput{SetStorageQuota: true, StorageQuotaBytes: int64ptr(1024)})
	if err != nil || u.StorageQuotaBytes == nil || *u.StorageQuotaBytes != 1024 {
		t.Fatalf("set quota = (%+v, %v), want 1024", u.StorageQuotaBytes, err)
	}
	// An update that does not mention the quota leaves it alone.
	u, err = svc.UpdateUser(ctx, admin, bob, UpdateUserInput{Role: strptr(RoleModerator)})
	if err != nil || u.StorageQuotaBytes == nil || *u.StorageQuotaBytes != 1024 {
		t.Fatalf("quota after unrelated edit = (%v, %v), want kept 1024", u.StorageQuotaBytes, err)
	}
	// Reset to the instance default (NULL).
	u, err = svc.UpdateUser(ctx, admin, bob, UpdateUserInput{SetStorageQuota: true})
	if err != nil || u.StorageQuotaBytes != nil {
		t.Fatalf("reset quota = (%v, %v), want nil", u.StorageQuotaBytes, err)
	}
	// Changing one's OWN quota is allowed (no lockout risk).
	if _, err := svc.UpdateUser(ctx, admin, admin, UpdateUserInput{SetStorageQuota: true, StorageQuotaBytes: int64ptr(5)}); err != nil {
		t.Errorf("self quota change = %v, want nil", err)
	}
}

// TestStats proves the admin overview aggregates surface each trivially-real
// count/sum from the repository unchanged.
func TestStats(t *testing.T) {
	repo := newFakeRepo()
	repo.add("ada", RoleAdmin)
	repo.add("bob", RoleUser)
	repo.publicVideos = 7
	repo.storedBytes = 4096
	repo.peers = 3
	repo.comments = 12
	svc := NewService(repo)

	got, err := svc.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	want := Stats{Users: 2, PublishedVideos: 7, MediaStoredBytes: 4096, FederatedPeers: 3, Comments: 12}
	if got != want {
		t.Errorf("Stats = %+v, want %+v", got, want)
	}
}

func TestStorageUsed(t *testing.T) {
	repo := newFakeRepo()
	bob := repo.add("bob", RoleUser)
	repo.used[bob] = 777
	svc := NewService(repo)
	if used, err := svc.StorageUsed(context.Background(), bob); err != nil || used != 777 {
		t.Errorf("StorageUsed = (%d, %v), want 777", used, err)
	}
}

func TestUpdateUserSelfGuardAndNotFound(t *testing.T) {
	repo := newFakeRepo()
	admin := repo.add("ada", RoleAdmin)
	svc := NewService(repo)
	ctx := context.Background()

	// Self-demotion and self-deactivation are rejected.
	if _, err := svc.UpdateUser(ctx, admin, admin, UpdateUserInput{Role: strptr(RoleUser)}); !errors.Is(err, ErrSelfChange) {
		t.Errorf("self-demote = %v, want ErrSelfChange", err)
	}
	if _, err := svc.UpdateUser(ctx, admin, admin, UpdateUserInput{IsActive: boolptr(false)}); !errors.Is(err, ErrSelfChange) {
		t.Errorf("self-deactivate = %v, want ErrSelfChange", err)
	}
	// Keeping your own role admin is fine (no-op-ish).
	if _, err := svc.UpdateUser(ctx, admin, admin, UpdateUserInput{Role: strptr(RoleAdmin)}); err != nil {
		t.Errorf("self keep-admin = %v, want nil", err)
	}
	// Unknown target.
	if _, err := svc.UpdateUser(ctx, admin, uuid.New(), UpdateUserInput{Role: strptr(RoleModerator)}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown = %v, want ErrNotFound", err)
	}
}

// TestUpdateUserEmailVerified proves the §10 admin edit at the service layer:
// email_verified flips on and off, other fields stay untouched, and flipping it
// on yourself is allowed (no lockout risk, unlike role/is_active).
func TestUpdateUserEmailVerified(t *testing.T) {
	repo := newFakeRepo()
	adminID := repo.add("ada", RoleAdmin)
	bobID := repo.add("bob", RoleUser)
	svc := NewService(repo)
	ctx := context.Background()

	updated, err := svc.UpdateUser(ctx, adminID, bobID, UpdateUserInput{EmailVerified: boolptr(true)})
	if err != nil {
		t.Fatalf("UpdateUser email_verified=true: %v", err)
	}
	if !updated.EmailVerified {
		t.Error("email_verified not set")
	}
	if updated.Role != RoleUser || !updated.IsActive {
		t.Errorf("unrelated fields changed: %+v", updated)
	}
	if repo.revoked[bobID] {
		t.Error("sessions revoked by an email_verified edit")
	}

	updated, err = svc.UpdateUser(ctx, adminID, bobID, UpdateUserInput{EmailVerified: boolptr(false)})
	if err != nil || updated.EmailVerified {
		t.Fatalf("revoke = %+v/%v, want verified=false", updated, err)
	}

	// Self-edit of email_verified is allowed (no lockout risk).
	if _, err := svc.UpdateUser(ctx, adminID, adminID, UpdateUserInput{EmailVerified: boolptr(true)}); err != nil {
		t.Errorf("self email_verified edit = %v, want allowed", err)
	}
}

// TestUpdateUserBypassQuarantine proves the §10/§11 admin edit at the service
// layer: bypass_quarantine flips on and off, other fields stay untouched, and
// no sessions are revoked by the edit.
func TestUpdateUserBypassQuarantine(t *testing.T) {
	repo := newFakeRepo()
	adminID := repo.add("ada", RoleAdmin)
	bobID := repo.add("bob", RoleUser)
	svc := NewService(repo)
	ctx := context.Background()

	updated, err := svc.UpdateUser(ctx, adminID, bobID, UpdateUserInput{BypassQuarantine: boolptr(true)})
	if err != nil {
		t.Fatalf("UpdateUser bypass_quarantine=true: %v", err)
	}
	if !updated.BypassQuarantine {
		t.Error("bypass_quarantine not set")
	}
	if updated.Role != RoleUser || !updated.IsActive || updated.EmailVerified {
		t.Errorf("unrelated fields changed: %+v", updated)
	}
	if repo.revoked[bobID] {
		t.Error("sessions revoked by a bypass_quarantine edit")
	}

	updated, err = svc.UpdateUser(ctx, adminID, bobID, UpdateUserInput{BypassQuarantine: boolptr(false)})
	if err != nil || updated.BypassQuarantine {
		t.Fatalf("revoke = %+v/%v, want bypass=false", updated, err)
	}
}

// TestUpdateUserProtectsTheInstanceOwner is A16 slice 1's finding turned into a
// test: an ordinary admin demoted the instance owner and got 200. The owner is
// now marked (0131) and another admin cannot take its role, its access or its
// account — while edits that carry no lockout risk still go through.
func TestUpdateUserProtectsTheInstanceOwner(t *testing.T) {
	repo := newFakeRepo()
	owner := repo.add("mona", RoleAdmin)
	repo.markOwner(owner)
	other := repo.add("avery", RoleAdmin)
	svc := NewService(repo)
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		in   UpdateUserInput
	}{
		{"demote to user", UpdateUserInput{Role: strptr(RoleUser)}},
		{"demote to moderator", UpdateUserInput{Role: strptr(RoleModerator)}},
		{"deactivate", UpdateUserInput{IsActive: boolptr(false)}},
	} {
		if _, err := svc.UpdateUser(ctx, other, owner, tc.in); !errors.Is(err, ErrOwnerProtected) {
			t.Errorf("%s by another admin = %v, want ErrOwnerProtected", tc.name, err)
		}
	}
	if got := repo.users[owner]; got.Role != RoleAdmin || !got.IsActive {
		t.Errorf("owner after the refusals = %s/%v, want admin/true", got.Role, got.IsActive)
	}
	if err := svc.CheckDelete(ctx, other, owner); !errors.Is(err, ErrOwnerProtected) {
		t.Errorf("delete owner by another admin = %v, want ErrOwnerProtected", err)
	}

	// Edits that cannot lock anybody out are still allowed on the owner.
	if _, err := svc.UpdateUser(ctx, other, owner, UpdateUserInput{SetStorageQuota: true, StorageQuotaBytes: int64ptr(1024)}); err != nil {
		t.Errorf("quota on the owner = %v, want allowed", err)
	}
	if _, err := svc.UpdateUser(ctx, other, owner, UpdateUserInput{EmailVerified: boolptr(true)}); err != nil {
		t.Errorf("email_verified on the owner = %v, want allowed", err)
	}
	// Re-asserting the role the owner already holds is a no-op, not a refusal.
	if _, err := svc.UpdateUser(ctx, other, owner, UpdateUserInput{Role: strptr(RoleAdmin)}); err != nil {
		t.Errorf("re-asserting admin on the owner = %v, want allowed", err)
	}

	// The owner's OWN self-guards are unchanged: it still gets ErrSelfChange,
	// not the new owner error, so the message it reads did not silently move.
	if _, err := svc.UpdateUser(ctx, owner, owner, UpdateUserInput{Role: strptr(RoleUser)}); !errors.Is(err, ErrSelfChange) {
		t.Errorf("owner self-demote = %v, want ErrSelfChange", err)
	}
	if _, err := svc.UpdateUser(ctx, owner, owner, UpdateUserInput{IsActive: boolptr(false)}); !errors.Is(err, ErrSelfChange) {
		t.Errorf("owner self-deactivate = %v, want ErrSelfChange", err)
	}
}

// TestCheckAdminRemovalKeepsOneAdmin pins the last-admin guard's arithmetic:
// only role='admin' AND is_active AND deleted_at IS NULL counts as a live
// administrator, so a deactivated or tombstoned admin is not the safety net
// that lets the last live one stand down.
func TestCheckAdminRemovalKeepsOneAdmin(t *testing.T) {
	ctx := context.Background()

	repo := newFakeRepo()
	sole := repo.add("mona", RoleAdmin)
	repo.add("bob", RoleUser)
	svc := NewService(repo)
	if err := svc.CheckAdminRemoval(ctx, sole); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("removing the only admin = %v, want ErrLastAdmin", err)
	}
	// A non-admin is never the last admin.
	if err := svc.CheckAdminRemoval(ctx, repo.add("cleo", RoleModerator)); err != nil {
		t.Errorf("removing a moderator = %v, want nil", err)
	}

	// A deactivated second admin cannot sign in, so it does not count.
	deactivated := repo.add("dana", RoleAdmin)
	du := repo.users[deactivated]
	du.IsActive = false
	repo.users[deactivated] = du
	if err := svc.CheckAdminRemoval(ctx, sole); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("with only a DEACTIVATED second admin = %v, want ErrLastAdmin", err)
	}

	// Neither does a tombstoned one.
	tomb := repo.add("erin", RoleAdmin)
	repo.tombstone(tomb)
	if err := svc.CheckAdminRemoval(ctx, sole); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("with only a TOMBSTONED second admin = %v, want ErrLastAdmin", err)
	}

	// A second live admin releases the guard.
	repo.add("fern", RoleAdmin)
	if err := svc.CheckAdminRemoval(ctx, sole); err != nil {
		t.Errorf("with a second live admin = %v, want nil", err)
	}
}

// TestUpdateUserRefusesRemovingTheLastAdmin covers the same guard on the admin
// route. Reaching it there needs a caller who is not themselves a live admin —
// impossible through requireRole today, which is exactly why it is defence in
// depth rather than the guard's only home.
func TestUpdateUserRefusesRemovingTheLastAdmin(t *testing.T) {
	repo := newFakeRepo()
	sole := repo.add("mona", RoleAdmin)
	caller := repo.add("script", RoleUser)
	svc := NewService(repo)
	ctx := context.Background()

	if _, err := svc.UpdateUser(ctx, caller, sole, UpdateUserInput{Role: strptr(RoleUser)}); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("demoting the last admin = %v, want ErrLastAdmin", err)
	}
	if _, err := svc.UpdateUser(ctx, caller, sole, UpdateUserInput{IsActive: boolptr(false)}); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("deactivating the last admin = %v, want ErrLastAdmin", err)
	}
	if err := svc.CheckDelete(ctx, caller, sole); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("deleting the last admin = %v, want ErrLastAdmin", err)
	}
	if got := repo.users[sole]; got.Role != RoleAdmin || !got.IsActive {
		t.Errorf("last admin after the refusals = %s/%v, want admin/true", got.Role, got.IsActive)
	}
}

// TestUpdateUserRefusesEveryWriteToATombstone closes A16 slice 1 finding (4):
// every field of a hard-deleted account other than is_active was still
// writable. The writes were inert, but "accepted and ignored" is the shape that
// lets a console report a change that did not happen.
func TestUpdateUserRefusesEveryWriteToATombstone(t *testing.T) {
	repo := newFakeRepo()
	adm := repo.add("mona", RoleAdmin)
	gone := repo.add("ghost", RoleUser)
	repo.tombstone(gone)
	before := repo.users[gone]
	svc := NewService(repo)
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		in   UpdateUserInput
	}{
		{"reactivate", UpdateUserInput{IsActive: boolptr(true)}},
		{"deactivate again", UpdateUserInput{IsActive: boolptr(false)}},
		{"role", UpdateUserInput{Role: strptr(RoleModerator)}},
		{"quota", UpdateUserInput{SetStorageQuota: true, StorageQuotaBytes: int64ptr(99)}},
		{"email_verified", UpdateUserInput{EmailVerified: boolptr(true)}},
		{"bypass_quarantine", UpdateUserInput{BypassQuarantine: boolptr(true)}},
		{"a combined body", UpdateUserInput{Role: strptr(RoleAdmin), SetStorageQuota: true, StorageQuotaBytes: int64ptr(7)}},
	} {
		if _, err := svc.UpdateUser(ctx, adm, gone, tc.in); !errors.Is(err, ErrDeletedAccount) {
			t.Errorf("%s on a tombstone = %v, want ErrDeletedAccount", tc.name, err)
		}
	}
	if got := repo.users[gone]; got != before {
		t.Errorf("tombstone changed:\n got %+v\nwant %+v", got, before)
	}
}
