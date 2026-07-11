package instancedocs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory Repository.
type fakeRepo struct {
	rows map[string]sqlcgen.InstanceDocument
}

func newFakeRepo() *fakeRepo { return &fakeRepo{rows: map[string]sqlcgen.InstanceDocument{}} }

func (f *fakeRepo) ListInstanceDocuments(context.Context) ([]sqlcgen.InstanceDocument, error) {
	out := make([]sqlcgen.InstanceDocument, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fakeRepo) UpsertInstanceDocument(_ context.Context, arg sqlcgen.UpsertInstanceDocumentParams) (sqlcgen.InstanceDocument, error) {
	row := sqlcgen.InstanceDocument{Name: arg.Name, Body: arg.Body, Sha256: arg.Sha256,
		UpdatedBy: arg.UpdatedBy, UpdatedAt: time.Now()}
	f.rows[arg.Name] = row
	return row, nil
}

func (f *fakeRepo) DeleteInstanceDocument(_ context.Context, name string) (int64, error) {
	if _, ok := f.rows[name]; !ok {
		return 0, nil
	}
	delete(f.rows, name)
	return 1, nil
}

func newLoadedService(t *testing.T) (*Service, *fakeRepo) {
	t.Helper()
	repo := newFakeRepo()
	svc := NewService(repo)
	if err := svc.Load(context.Background()); err != nil {
		t.Fatalf("load: %v", err)
	}
	return svc, repo
}

func TestSetGetRoundTripAndHash(t *testing.T) {
	svc, _ := newLoadedService(t)

	if _, ok := svc.Get(NameHomepage); ok {
		t.Fatal("fresh store reports a homepage document")
	}
	if h := svc.Hash(NameCustomCSS); h != "" {
		t.Fatalf("hash of unset doc = %q, want empty", h)
	}

	body := "# Welcome\n\nHello."
	doc, err := svc.Set(context.Background(), NameHomepage, body, uuid.New())
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	sum := sha256.Sum256([]byte(body))
	wantHash := hex.EncodeToString(sum[:])
	if doc.Body != body || doc.SHA256 != wantHash {
		t.Errorf("set returned body=%q hash=%q, want body round-trip + sha256 %q", doc.Body, doc.SHA256, wantHash)
	}
	got, ok := svc.Get(NameHomepage)
	if !ok || got.Body != body || got.SHA256 != wantHash {
		t.Errorf("get = %+v ok=%v, want stored doc", got, ok)
	}
	if svc.Hash(NameHomepage) != wantHash {
		t.Errorf("hash = %q, want %q", svc.Hash(NameHomepage), wantHash)
	}
}

func TestSetEmptyBodyClears(t *testing.T) {
	svc, repo := newLoadedService(t)
	if _, err := svc.Set(context.Background(), NameCustomJS, "alert(1)", uuid.New()); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := svc.Set(context.Background(), NameCustomJS, "", uuid.New()); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, ok := svc.Get(NameCustomJS); ok {
		t.Error("document still set after clearing with an empty body")
	}
	if len(repo.rows) != 0 {
		t.Errorf("repo rows = %d after clear, want 0", len(repo.rows))
	}
}

func TestSetUnknownNameAndCaps(t *testing.T) {
	svc, _ := newLoadedService(t)

	if _, err := svc.Set(context.Background(), "nope", "x", uuid.Nil); !errors.Is(err, ErrUnknownName) {
		t.Errorf("unknown name err = %v, want ErrUnknownName", err)
	}

	// Per-document caps: homepage 100KB, css/js 200KB.
	cases := []struct {
		name string
		cap  int
	}{
		{NameHomepage, 100 << 10},
		{NameCustomCSS, 200 << 10},
		{NameCustomJS, 200 << 10},
	}
	for _, tc := range cases {
		if got := MaxBodyBytes(tc.name); got != tc.cap {
			t.Errorf("MaxBodyBytes(%s) = %d, want %d", tc.name, got, tc.cap)
		}
		over := strings.Repeat("a", tc.cap+1)
		var ve *ValidationError
		if _, err := svc.Set(context.Background(), tc.name, over, uuid.Nil); !errors.As(err, &ve) {
			t.Errorf("%s over-cap err = %v, want *ValidationError", tc.name, err)
		}
		atCap := strings.Repeat("a", tc.cap)
		if _, err := svc.Set(context.Background(), tc.name, atCap, uuid.Nil); err != nil {
			t.Errorf("%s at-cap set failed: %v", tc.name, err)
		}
	}
}

func TestLoadPopulatesCacheAndSkipsUnknownRows(t *testing.T) {
	repo := newFakeRepo()
	repo.rows[NameCustomCSS] = sqlcgen.InstanceDocument{Name: NameCustomCSS, Body: "body{}", Sha256: "abc"}
	repo.rows["legacy"] = sqlcgen.InstanceDocument{Name: "legacy", Body: "x", Sha256: "y"}
	svc := NewService(repo)
	if err := svc.Load(context.Background()); err != nil {
		t.Fatalf("load: %v", err)
	}
	if d, ok := svc.Get(NameCustomCSS); !ok || d.Body != "body{}" || d.SHA256 != "abc" {
		t.Errorf("css after load = %+v ok=%v", d, ok)
	}
	if _, ok := svc.Get("legacy"); ok {
		t.Error("unknown legacy row surfaced from the cache")
	}
}

func TestNilRepoIsInert(t *testing.T) {
	svc := NewService(nil)
	if err := svc.Load(context.Background()); err != nil {
		t.Fatalf("load with nil repo: %v", err)
	}
	if _, ok := svc.Get(NameHomepage); ok {
		t.Error("nil-repo service reports a document")
	}
	if _, err := svc.Set(context.Background(), NameHomepage, "x", uuid.Nil); err == nil {
		t.Error("set with nil repo succeeded, want error")
	}
}
