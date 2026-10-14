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
	res, err := svc.SyncNow(ctx, sub.ID, admin.ID, false)
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
	if res, _ := svc.SyncNow(ctx, other.ID, uuid.Nil, false); res.Added != 1 {
		t.Fatalf("other sync = %+v", res)
	}

	// Sync 2: the list drops b and manual, adds e. Only b goes.
	ff.body = "#domain,#severity\n" + d("a") + ",suspend\n" + d("e") + ",suspend\n"
	unblocked = nil
	res, err = svc.SyncNow(ctx, sub.ID, admin.ID, false)
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
	res, _ = svc.SyncNow(ctx, sub.ID, admin.ID, false)
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
	if res, _ := svc.SyncNow(ctx, sub.ID, admin.ID, false); res.Status != "not_modified" {
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

func TestSyncSafetyRails(t *testing.T) {
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
	admin, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{Username: "sr_" + suf, Email: "sr_" + suf + "@example.test", PasswordHash: "x", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	ff := &fakeFetcher{etag: `"v1"`, notMod: true}
	self := "vidra-" + suf + ".sub.example"
	svc := blocklistsub.NewService(q, ff, nil, blocklistsub.WithProtectedHost("https://"+self+"/"))
	owner := func(dom string) string {
		var src string
		_ = st.Pool.QueryRow(ctx, `SELECT source FROM blocked_instances WHERE domain=$1`, dom).Scan(&src)
		return src
	}
	sub, err := svc.Create(ctx, admin.ID, "https://lists.example/sr-"+suf+".txt", "", blocklistsub.FormatDomainList, true)
	if err != nil {
		t.Fatal(err)
	}

	// The instance's own host and its parent domain are never blocked.
	parent := self[strings.Index(self, ".")+1:]
	ff.body = d("a") + "\n" + self + "\n" + parent + "\n"
	res, _ := svc.SyncNow(ctx, sub.ID, admin.ID, false)
	if res.Status != "ok" || res.Added != 1 || res.Protected != 2 || owner(self) != "" || owner(parent) != "" {
		t.Fatalf("protected host: %+v self=%q parent=%q", res, owner(self), owner(parent))
	}

	// An allowlist removal re-applies even though the list's ETag is stable (304).
	if err := svc.AddAllow(ctx, admin.ID, d("a")); err != nil {
		t.Fatal(err)
	}
	if owner(d("a")) != "" {
		t.Fatal("allowlisted block should be lifted")
	}
	if err := svc.RemoveAllow(ctx, d("a")); err != nil {
		t.Fatal(err)
	}
	res, _ = svc.SyncNow(ctx, sub.ID, admin.ID, false)
	if res.Status != "ok" || owner(d("a")) != "subscription:"+sub.ID.String() {
		t.Fatalf("allow removal not re-applied on a stable ETag: %+v owner=%q", res, owner(d("a")))
	}

	// IDN allowlist entries match the punycode the lists are folded to.
	ff.notMod = false
	ff.body = "b\u00fccher-" + suf + ".example\n" + d("keep") + "\n"
	if err := svc.AddAllow(ctx, admin.ID, "B\u00fccher-"+suf+".example"); err != nil {
		t.Fatal(err)
	}
	res, _ = svc.SyncNow(ctx, sub.ID, admin.ID, false)
	var n int
	_ = st.Pool.QueryRow(ctx, `SELECT count(*) FROM blocked_instances WHERE domain LIKE 'xn--%' || $1 || '%'`, suf).Scan(&n)
	if n != 0 {
		t.Errorf("an IDN allowlist entry did not exempt the punycode domain (%+v)", res)
	}

	// Removal cap: seed 60 owned blocks, then a list naming 1 of them is held.
	var sb strings.Builder
	for i := 0; i < 60; i++ {
		sb.WriteString(d("bulk"+string(rune('a'+i/26))+string(rune('a'+i%26))) + "\n")
	}
	ff.body = sb.String()
	if res, _ = svc.SyncNow(ctx, sub.ID, admin.ID, false); res.Status != "ok" {
		t.Fatalf("seed: %+v", res)
	}
	ff.body = d("bulkaa") + "\n"
	res, _ = svc.SyncNow(ctx, sub.ID, admin.ID, false)
	if res.Status != "needs_confirmation" || res.Pending < 59 || owner(d("bulkab")) == "" {
		t.Fatalf("cap did not hold the sync: %+v", res)
	}
	got, _ := q.GetBlocklistSubscription(ctx, sub.ID)
	if got.LastStatus != "needs_confirmation" {
		t.Errorf("status = %q", got.LastStatus)
	}
	// Confirming applies it.
	res, _ = svc.SyncNow(ctx, sub.ID, admin.ID, true)
	if res.Status != "ok" || res.Removed < 59 || owner(d("bulkab")) != "" || owner(d("bulkaa")) == "" {
		t.Fatalf("confirmed sync: %+v", res)
	}
}
