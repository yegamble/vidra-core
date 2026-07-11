package quota

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func i64(n int64) *int64 { return &n }

func TestEffectiveResolution(t *testing.T) {
	cases := []struct {
		name            string
		override        *int64
		instanceDefault int64
		want            *int64 // nil = unlimited
	}{
		{"no override, unlimited instance", nil, 0, nil},
		{"no override, instance default applies", nil, 100, i64(100)},
		{"override wins over instance default", i64(50), 100, i64(50)},
		{"override 0 = unlimited even with a finite default", i64(0), 100, nil},
		{"override applies when instance is unlimited", i64(50), 0, i64(50)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Effective(tc.override, tc.instanceDefault)
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("Effective(%v, %d) = %d, want unlimited (nil)", tc.override, tc.instanceDefault, *got)
			case tc.want != nil && (got == nil || *got != *tc.want):
				t.Errorf("Effective(%v, %d) = %v, want %d", tc.override, tc.instanceDefault, got, *tc.want)
			}
		})
	}
}

// fakeRepo is an in-memory quota.Repository.
type fakeRepo struct {
	users map[uuid.UUID]sqlcgen.User
	used  map[uuid.UUID]int64
}

func (f *fakeRepo) GetUserByID(_ context.Context, id uuid.UUID) (sqlcgen.User, error) {
	u, ok := f.users[id]
	if !ok {
		return sqlcgen.User{}, errors.New("not found")
	}
	return u, nil
}

func (f *fakeRepo) SumUserStorageUsage(_ context.Context, ownerID uuid.UUID) (int64, error) {
	return f.used[ownerID], nil
}

func seed(override *int64, used int64) (*fakeRepo, uuid.UUID) {
	id := uuid.New()
	return &fakeRepo{
		users: map[uuid.UUID]sqlcgen.User{id: {ID: id, StorageQuotaBytes: override}},
		used:  map[uuid.UUID]int64{id: used},
	}, id
}

// TestDefaultBytesFuncOverlay proves WithDefaultBytesFunc supersedes the
// constructor default and is resolved live per Status (the instance-settings
// overlay), while a per-user override still wins over it.
func TestDefaultBytesFuncOverlay(t *testing.T) {
	ctx := context.Background()
	repo, id := seed(nil, 10) // no per-user override → the instance default applies

	current := int64(100)
	svc := NewService(repo, 999, WithDefaultBytesFunc(func() int64 { return current }))

	// The func supersedes the constructor's 999.
	if st, _ := svc.Status(ctx, id); st.QuotaBytes == nil || *st.QuotaBytes != 100 {
		t.Fatalf("Status.QuotaBytes = %v, want 100 (from the func, not 999)", st.QuotaBytes)
	}
	// It is resolved live: a later value takes effect with no reconstruction.
	current = 50
	if st, _ := svc.Status(ctx, id); st.QuotaBytes == nil || *st.QuotaBytes != 50 {
		t.Fatalf("Status.QuotaBytes after change = %v, want 50", st.QuotaBytes)
	}
	// 0 from the func means unlimited.
	current = 0
	if st, _ := svc.Status(ctx, id); st.QuotaBytes != nil {
		t.Errorf("func 0 should be unlimited, got %d", *st.QuotaBytes)
	}

	// A per-user override still wins over the instance-default func.
	repoOv, idOv := seed(i64(70), 10)
	svcOv := NewService(repoOv, 999, WithDefaultBytesFunc(func() int64 { return 500 }))
	if st, _ := svcOv.Status(ctx, idOv); st.QuotaBytes == nil || *st.QuotaBytes != 70 {
		t.Fatalf("per-user override Status.QuotaBytes = %v, want 70", st.QuotaBytes)
	}
}

func TestStatusAndRemaining(t *testing.T) {
	ctx := context.Background()

	// Limited by the instance default.
	repo, id := seed(nil, 30)
	svc := NewService(repo, 100)
	st, err := svc.Status(ctx, id)
	if err != nil || st.UsedBytes != 30 || st.QuotaBytes == nil || *st.QuotaBytes != 100 {
		t.Fatalf("Status = (%+v, %v), want used=30 quota=100", st, err)
	}
	rem, limited, err := svc.Remaining(ctx, id)
	if err != nil || !limited || rem != 70 {
		t.Fatalf("Remaining = (%d, %v, %v), want (70, true, nil)", rem, limited, err)
	}

	// Unlimited: no cap reported.
	repo, id = seed(nil, 30)
	svc = NewService(repo, 0)
	st, _ = svc.Status(ctx, id)
	if st.QuotaBytes != nil {
		t.Errorf("unlimited Status.QuotaBytes = %d, want nil", *st.QuotaBytes)
	}
	if _, limited, _ := svc.Remaining(ctx, id); limited {
		t.Errorf("unlimited Remaining reported limited=true")
	}

	// Already over quota (e.g. the admin lowered it): remaining clamps at 0.
	repo, id = seed(i64(10), 25)
	svc = NewService(repo, 0)
	rem, limited, _ = svc.Remaining(ctx, id)
	if !limited || rem != 0 {
		t.Errorf("over-quota Remaining = (%d, %v), want (0, true)", rem, limited)
	}

	// Unknown user surfaces the repo error.
	if _, err := svc.Status(ctx, uuid.New()); err == nil {
		t.Errorf("Status(unknown) = nil error, want error")
	}
}

func TestCheckFits(t *testing.T) {
	ctx := context.Background()
	repo, id := seed(i64(100), 90)
	svc := NewService(repo, 0)

	if err := svc.CheckFits(ctx, id, 10); err != nil {
		t.Errorf("exactly-at-limit CheckFits = %v, want nil", err)
	}
	if err := svc.CheckFits(ctx, id, 11); !errors.Is(err, ErrExceeded) {
		t.Errorf("over-limit CheckFits = %v, want ErrExceeded", err)
	}

	// Unlimited never rejects.
	repo, id = seed(nil, 1<<40)
	svc = NewService(repo, 0)
	if err := svc.CheckFits(ctx, id, 1<<40); err != nil {
		t.Errorf("unlimited CheckFits = %v, want nil", err)
	}
}
