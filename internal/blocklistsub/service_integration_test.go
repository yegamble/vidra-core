//go:build integration

package blocklistsub_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/blocklistsub"
	"github.com/vidra/vidra-core/internal/instancemod"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type fakeFetcher struct {
	body, etag string
	notMod     bool
	err        error
}

func (f *fakeFetcher) Fetch(_ context.Context, _, etag string) (blocklistsub.Result, error) {
	if f.err != nil {
		return blocklistsub.Result{}, f.err
	}
	if f.notMod && etag != "" {
		return blocklistsub.Result{NotModified: true}, nil
	}
	return blocklistsub.Result{Body: []byte(f.body), ETag: f.etag}, nil
}

type auditBuf struct {
	mu  sync.Mutex
	evs []audit.Event
}

func (a *auditBuf) Record(_ context.Context, ev audit.Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.evs = append(a.evs, ev)
	return nil
}

func TestSyncOwnsOnlyItsBlocks(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	q := st.Queries()
	suf := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	d := func(n string) string { return n + "-" + suf + ".example" }
	admin, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{Username: "bl_" + suf, Email: "bl_" + suf + "@example.test", PasswordHash: "x", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	mod := instancemod.NewService(q)
	ff := &fakeFetcher{etag: `"e1"`}
	ab := &auditBuf{}
	var unblocked []string
	svc := blocklistsub.NewService(q, ff, nil, blocklistsub.WithAuditor(ab),
		blocklistsub.WithOnUnblocked(func(_ context.Context, dom string, _ time.Time) { unblocked = append(unblocked, dom) }))

	owner := func(dom string) string {
		var src string
		_ = st.Pool.QueryRow(ctx, `SELECT source FROM blocked_instances WHERE domain=$1`, dom).Scan(&src)
		return src
	}

	// A manual block that the list also names, and one it does not.
	if err := mod.BlockInstance(ctx, admin.ID, d("manual"), "by hand"); err != nil {
		t.Fatal(err)
	}
	if err := mod.BlockInstance(ctx, admin.ID, d("manualonly"), "by hand"); err != nil {
		t.Fatal(err)
	}

	sub, err := svc.Create(ctx, admin.ID, "https://lists.example/one.csv", "Test list", blocklistsub.FormatMastodonCSV, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, admin.ID, "https://lists.example/one.csv", "", blocklistsub.FormatJSON, true); err != blocklistsub.ErrDuplicate {
		t.Fatalf("duplicate URL: %v", err)
	}
	other, _ := svc.Create(ctx, admin.ID, "https://lists.example/two.csv", "", blocklistsub.FormatDomainList, true)

	// Sync 1: a, b, manual (already blocked), allowed (allowlisted), silenced (ignored).
	if err := svc.AddAllow(ctx, admin.ID, d("allowed")); err != nil {
		t.Fatal(err)
	}
	ff.body = "#domain,#severity\n" + d("a") + ",suspend\n" + d("b") + ",suspend\n" + d("manual") + ",suspend\n" +
		d("allowed") + ",suspend\n" + d("silenced") + ",silence\n"
	res, err := svc.SyncNow(ctx, sub.ID, admin.ID)
	if err != nil || res.Status != "ok" || res.Added != 2 || res.Removed != 0 || res.IgnoredSeverity != 1 {
		t.Fatalf("sync1 = %+v %v", res, err)
	}
	mine := "subscription:" + sub.ID.String()
	if owner(d("a")) != mine || owner(d("b")) != mine {
		t.Error("a/b should be owned by the subscription")
	}
	if owner(d("manual")) != "manual" {
		t.Errorf("manual block was taken over: %q", owner(d("manual")))
	}
	if owner(d("allowed")) != "" || owner(d("silenced")) != "" {
		t.Error("allowlisted and silence-severity entries must not be blocked")
	}

	// Another subscription's block must survive this one's removals.
	ff.body = d("c") + "\n"
	if _, err := q.GetBlocklistSubscription(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if res, _ := svc.SyncNow(ctx, other.ID, uuid.Nil); res.Added != 1 {
		t.Fatalf("other sync = %+v", res)
	}

	// Sync 2: the list drops b and manual, adds e. Only b goes.
	ff.body = "#domain,#severity\n" + d("a") + ",suspend\n" + d("e") + ",suspend\n"
	unblocked = nil
	res, err = svc.SyncNow(ctx, sub.ID, admin.ID)
	if err != nil || res.Added != 1 || res.Removed != 1 {
		t.Fatalf("sync2 = %+v %v", res, err)
	}
	if owner(d("b")) != "" {
		t.Error("b should have been removed")
	}
	if owner(d("manual")) != "manual" || owner(d("manualonly")) != "manual" {
		t.Error("manual blocks must survive a list removal")
	}
	if owner(d("c")) != "subscription:"+other.ID.String() {
		t.Error("another subscription's block was touched")
	}
	if len(unblocked) != 1 || unblocked[0] != d("b") {
		t.Errorf("unblock hook = %v", unblocked)
	}

	// A list that yields nothing usable removes NOTHING and reports an error.
	ff.body = "<html>oops</html>"
	res, _ = svc.SyncNow(ctx, sub.ID, admin.ID)
	if res.Status != "error" || owner(d("a")) != mine || owner(d("e")) != mine {
		t.Errorf("empty-list guard failed: %+v", res)
	}
	got, _ := q.GetBlocklistSubscription(ctx, sub.ID)
	if got.LastStatus != "error" || got.Etag != `"e1"` || got.LastAdded != 1 {
		t.Errorf("failed attempt must keep last good state: %+v", got)
	}

	// Allowlisting a subscription-owned domain lifts it now; a manual one stays.
	if err := svc.AddAllow(ctx, admin.ID, d("a")); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddAllow(ctx, admin.ID, d("manualonly")); err != nil {
		t.Fatal(err)
	}
	if owner(d("a")) != "" || owner(d("manualonly")) != "manual" {
		t.Error("allowlist must lift a subscription block but never a manual one")
	}

	// A manual re-block of a subscription-owned domain adopts it.
	if err := mod.BlockInstance(ctx, admin.ID, d("e"), "adopt"); err != nil {
		t.Fatal(err)
	}
	if owner(d("e")) != "manual" {
		t.Error("manual block should adopt the row")
	}

	// 304: no change, status recorded.
	ff.notMod = true
	if res, _ := svc.SyncNow(ctx, sub.ID, admin.ID); res.Status != "not_modified" {
		t.Errorf("304 = %+v", res)
	}

	// Audit: one summary per sync, counts only.
	var sawSync2 bool
	for _, ev := range ab.evs {
		for _, m := range ev.Metadata {
			if m.Key == "added" && m.Value == "1" && ev.ResourceID == sub.ID.String() {
				sawSync2 = true
			}
		}
		if strings.Contains(ev.Reason, "example") {
			t.Errorf("audit leaked a domain: %q", ev.Reason)
		}
	}
	if !sawSync2 || len(ab.evs) < 5 {
		t.Errorf("audit events = %d sawSync2=%v", len(ab.evs), sawSync2)
	}

	// Deleting the subscription removes ONLY what it owns.
	if n, err := svc.Delete(ctx, other.ID); err != nil || n != 1 {
		t.Fatalf("delete other: %d %v", n, err)
	}
	if n, err := svc.Delete(ctx, sub.ID); err != nil || n != 0 {
		// e was adopted, a was allowlisted away, b removed -> nothing left to drop.
		t.Fatalf("delete sub: %d %v", n, err)
	}
	if owner(d("e")) != "manual" || owner(d("manual")) != "manual" || owner(d("c")) != "" {
		t.Error("delete touched a block it did not own (or kept one it owned)")
	}
}
