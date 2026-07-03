package instancemod

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory Repository. Maps/slices mutate through the pointer
// receiver.
type fakeRepo struct {
	mutes   map[string]time.Time // "<muter>|<domain>"
	blocked map[string]sqlcgen.BlockedInstance
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{mutes: map[string]time.Time{}, blocked: map[string]sqlcgen.BlockedInstance{}}
}

func (f *fakeRepo) MuteInstance(_ context.Context, a sqlcgen.MuteInstanceParams) (int64, error) {
	k := a.MuterID.String() + "|" + a.Domain
	if _, ok := f.mutes[k]; ok {
		return 0, nil
	}
	f.mutes[k] = time.Now()
	return 1, nil
}

func (f *fakeRepo) UnmuteInstance(_ context.Context, a sqlcgen.UnmuteInstanceParams) (int64, error) {
	k := a.MuterID.String() + "|" + a.Domain
	if _, ok := f.mutes[k]; !ok {
		return 0, nil
	}
	delete(f.mutes, k)
	return 1, nil
}

func (f *fakeRepo) ListMutedInstances(_ context.Context, a sqlcgen.ListMutedInstancesParams) ([]sqlcgen.ListMutedInstancesRow, error) {
	var rows []sqlcgen.ListMutedInstancesRow
	prefix := a.MuterID.String() + "|"
	for k, at := range f.mutes {
		if strings.HasPrefix(k, prefix) {
			rows = append(rows, sqlcgen.ListMutedInstancesRow{Domain: strings.TrimPrefix(k, prefix), CreatedAt: at})
		}
	}
	return rows, nil
}

func (f *fakeRepo) BlockInstance(_ context.Context, a sqlcgen.BlockInstanceParams) (int64, error) {
	f.blocked[a.Domain] = sqlcgen.BlockedInstance{
		Domain: a.Domain, Reason: a.Reason, BlockedBy: a.BlockedBy, CreatedAt: time.Now(),
	}
	return 1, nil
}

func (f *fakeRepo) UnblockInstance(_ context.Context, domain string) (int64, error) {
	if _, ok := f.blocked[domain]; !ok {
		return 0, nil
	}
	delete(f.blocked, domain)
	return 1, nil
}

func (f *fakeRepo) ListBlockedInstances(_ context.Context, _ sqlcgen.ListBlockedInstancesParams) ([]sqlcgen.BlockedInstance, error) {
	var rows []sqlcgen.BlockedInstance
	for _, b := range f.blocked {
		rows = append(rows, b)
	}
	return rows, nil
}

func TestNormalizeDomain(t *testing.T) {
	valid := map[string]string{
		"Remote.Example":        "remote.example",
		"  tube.example  ":      "tube.example",
		"localhost:8088":        "localhost:8088",
		"video.example.org":     "video.example.org",
		"xn--bcher-kva.example": "xn--bcher-kva.example",
	}
	for in, want := range valid {
		got, err := NormalizeDomain(in)
		if err != nil || got != want {
			t.Errorf("NormalizeDomain(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	invalid := []string{
		"", "https://remote.example", "remote.example/path", "a b.example",
		"user@remote.example", "remote.example?x=1", "remote.example#f",
		strings.Repeat("a", 254) + ".example",
	}
	for _, in := range invalid {
		if _, err := NormalizeDomain(in); err == nil {
			t.Errorf("NormalizeDomain(%q) accepted, want ErrInvalidDomain", in)
		}
	}
}

func TestMuteUnmuteListInstances(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	user := uuid.New()
	ctx := context.Background()

	if err := svc.MuteInstance(ctx, user, "Spam.Example"); err != nil {
		t.Fatalf("MuteInstance: %v", err)
	}
	// Idempotent re-mute.
	if err := svc.MuteInstance(ctx, user, "spam.example"); err != nil {
		t.Fatalf("re-MuteInstance: %v", err)
	}
	items, err := svc.ListMutedInstances(ctx, user, 20, 0)
	if err != nil {
		t.Fatalf("ListMutedInstances: %v", err)
	}
	if len(items) != 1 || items[0].Domain != "spam.example" {
		t.Fatalf("items = %+v, want one normalized spam.example", items)
	}
	if err := svc.UnmuteInstance(ctx, user, "SPAM.example"); err != nil {
		t.Fatalf("UnmuteInstance: %v", err)
	}
	if items, _ := svc.ListMutedInstances(ctx, user, 20, 0); len(items) != 0 {
		t.Errorf("after unmute items = %+v, want none", items)
	}
	if err := svc.MuteInstance(ctx, user, "https://spam.example"); err != ErrInvalidDomain {
		t.Errorf("mute of a URL = %v, want ErrInvalidDomain", err)
	}
}

func TestBlockUnblockListInstances(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo)
	adm := uuid.New()
	ctx := context.Background()

	longReason := strings.Repeat("r", maxReasonLen+50)
	if err := svc.BlockInstance(ctx, adm, "Bad.Example", longReason); err != nil {
		t.Fatalf("BlockInstance: %v", err)
	}
	items, err := svc.ListBlockedInstances(ctx, 20, 0)
	if err != nil {
		t.Fatalf("ListBlockedInstances: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v, want 1", items)
	}
	if items[0].Domain != "bad.example" {
		t.Errorf("domain = %q, want normalized bad.example", items[0].Domain)
	}
	if len(items[0].Reason) != maxReasonLen {
		t.Errorf("reason length = %d, want bounded to %d", len(items[0].Reason), maxReasonLen)
	}
	if items[0].BlockedBy == nil || *items[0].BlockedBy != adm {
		t.Errorf("blocked_by = %v, want %s", items[0].BlockedBy, adm)
	}
	if err := svc.UnblockInstance(ctx, "bad.example"); err != nil {
		t.Fatalf("UnblockInstance: %v", err)
	}
	if items, _ := svc.ListBlockedInstances(ctx, 20, 0); len(items) != 0 {
		t.Errorf("after unblock items = %+v, want none", items)
	}
	if err := svc.BlockInstance(ctx, adm, "bad domain", ""); err != ErrInvalidDomain {
		t.Errorf("block of an invalid domain = %v, want ErrInvalidDomain", err)
	}
}
