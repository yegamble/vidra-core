package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/blocklistsub"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// blFakeRepo implements only what the handlers reach; the embedded nil
// Repository turns any other call into a loud panic.
type blFakeRepo struct {
	blocklistsub.Repository
	subs  map[uuid.UUID]sqlcgen.BlocklistSubscription
	allow map[string]bool
}

func (f *blFakeRepo) CreateBlocklistSubscription(_ context.Context, a sqlcgen.CreateBlocklistSubscriptionParams) (sqlcgen.BlocklistSubscription, error) {
	for _, s := range f.subs {
		if s.Url == a.Url {
			return sqlcgen.BlocklistSubscription{}, &pgconn.PgError{Code: "23505"}
		}
	}
	s := sqlcgen.BlocklistSubscription{ID: uuid.New(), Url: a.Url, Label: a.Label, Format: a.Format, Enabled: a.Enabled, LastStatus: "never", CreatedAt: time.Now()}
	f.subs[s.ID] = s
	return s, nil
}
func (f *blFakeRepo) GetBlocklistSubscription(_ context.Context, id uuid.UUID) (sqlcgen.BlocklistSubscription, error) {
	if s, ok := f.subs[id]; ok {
		return s, nil
	}
	return sqlcgen.BlocklistSubscription{}, pgx.ErrNoRows
}
func (f *blFakeRepo) DeleteBlocklistSubscription(_ context.Context, id uuid.UUID) (int64, error) {
	if _, ok := f.subs[id]; !ok {
		return 0, nil
	}
	delete(f.subs, id)
	return 1, nil
}
func (f *blFakeRepo) DeleteAllSubscriptionBlocksForSource(context.Context, string) ([]sqlcgen.DeleteAllSubscriptionBlocksForSourceRow, error) {
	return nil, nil
}
func (f *blFakeRepo) ListBlocklistSubscriptions(context.Context, sqlcgen.ListBlocklistSubscriptionsParams) ([]sqlcgen.ListBlocklistSubscriptionsRow, error) {
	var rows []sqlcgen.ListBlocklistSubscriptionsRow
	for _, s := range f.subs {
		rows = append(rows, sqlcgen.ListBlocklistSubscriptionsRow{ID: s.ID, Url: s.Url, Format: s.Format, Enabled: s.Enabled, LastStatus: s.LastStatus})
	}
	return rows, nil
}
func (f *blFakeRepo) CountBlocklistSubscriptions(context.Context) (int64, error) {
	return int64(len(f.subs)), nil
}
func (f *blFakeRepo) RecordBlocklistSyncResult(context.Context, sqlcgen.RecordBlocklistSyncResultParams) error {
	return nil
}
func (f *blFakeRepo) AddBlocklistAllow(_ context.Context, a sqlcgen.AddBlocklistAllowParams) (int64, error) {
	f.allow[a.Domain] = true
	return 1, nil
}
func (f *blFakeRepo) CountSubscriptionBlocks(context.Context, string) (int64, error) { return 7, nil }
func (f *blFakeRepo) DeleteSubscriptionBlocksForDomain(context.Context, string) ([]sqlcgen.DeleteSubscriptionBlocksForDomainRow, error) {
	return nil, nil
}

type blFailFetcher struct{}

func (blFailFetcher) Fetch(context.Context, string, string) (blocklistsub.Result, error) {
	return blocklistsub.Result{}, blocklistsub.StatusError{Code: 503}
}

type blAuditBuf struct{ evs []audit.Event }

func (b *blAuditBuf) Record(_ context.Context, ev audit.Event) error {
	b.evs = append(b.evs, ev)
	return nil
}

func blServer(t *testing.T) (*Server, *blFakeRepo) {
	t.Helper()
	repo := &blFakeRepo{subs: map[uuid.UUID]sqlcgen.BlocklistSubscription{}, allow: map[string]bool{}}
	f := blocklistsub.NewHTTPFetcher(false)
	svc := blocklistsub.NewService(repo, blFailFetcher{}, f.ValidateURL, blocklistsub.WithAuditor(&blAuditBuf{}))
	srv, _, _, _, _ := videoServerFullWith(t, testConfig(), []Option{WithBlocklistSubscriptionService(svc)})
	return srv, repo
}

const blBase = "/api/v1/admin/instances/blocklist-subscriptions"

func TestBlocklistSubscriptionsAdminOnly(t *testing.T) {
	srv, _ := blServer(t)
	_, _ = registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bob, _ := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	id := uuid.NewString()
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, blBase}, {http.MethodPost, blBase}, {http.MethodPatch, blBase + "/" + id},
		{http.MethodDelete, blBase + "/" + id}, {http.MethodPost, blBase + "/" + id + "/sync"},
		{http.MethodGet, "/api/v1/admin/instances/blocklist-allowlist"},
		{http.MethodPost, "/api/v1/admin/instances/blocklist-allowlist"},
		{http.MethodDelete, "/api/v1/admin/instances/blocklist-allowlist/x.example"},
	} {
		if rec := sendJSONAuth(srv, c.method, c.path, `{}`, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("anon %s %s = %d, want 401", c.method, c.path, rec.Code)
		}
		if rec := sendJSONAuth(srv, c.method, c.path, `{}`, bob); rec.Code != http.StatusForbidden {
			t.Errorf("user %s %s = %d, want 403", c.method, c.path, rec.Code)
		}
	}
}

func TestBlocklistSubscriptionLifecycle(t *testing.T) {
	srv, _ := blServer(t)
	admin, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	for _, body := range []string{
		`{"url":"https://lists.example/a.csv","format":"xml"}`,
		`{"url":"","format":"json"}`,
		`{"url":"file:///etc/passwd","format":"json"}`,
		`{"url":"http://169.254.169.254/x","format":"json"}`,
		`{"url":"https://u:p@lists.example/a.csv","format":"json"}`,
	} {
		if rec := sendJSONAuth(srv, http.MethodPost, blBase, body, admin); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("create %s = %d, want 422", body, rec.Code)
		}
	}
	rec := sendJSONAuth(srv, http.MethodPost, blBase, `{"url":"https://lists.example/a.csv","format":"mastodon_csv","label":"A"}`, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	var created blocklistSubscriptionView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if !created.Enabled || created.LastStatus != "never" {
		t.Errorf("created = %+v", created)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, blBase, `{"url":"https://lists.example/a.csv","format":"json"}`, admin); rec.Code != http.StatusConflict {
		t.Errorf("duplicate = %d, want 409", rec.Code)
	}
	var list blocklistSubscriptionListResponse
	_ = json.Unmarshal(getWithAuth(srv, blBase, admin).Body.Bytes(), &list)
	if len(list.Subscriptions) != 1 || list.Total != 1 {
		t.Errorf("list = %+v", list)
	}
	rec = sendJSONAuth(srv, http.MethodPatch, blBase+"/"+created.ID, `{"enabled":false}`, admin)
	var patched blocklistSubscriptionView
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if rec.Code != http.StatusOK || patched.BlockCount != 7 || patched.Enabled {
		t.Errorf("patch = %d %+v (want the real block_count)", rec.Code, patched)
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, blBase+"/not-a-uuid", `{}`, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id = %d, want 400", rec.Code)
	}

	// A failing fetch is a 200 with status error and a class message only.
	rec = sendJSONAuth(srv, http.MethodPost, blBase+"/"+created.ID+"/sync", ``, admin)
	var res blocklistSyncResultView
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || res.Status != "error" || res.Error != "HTTP 503" {
		t.Errorf("sync = %d %+v", rec.Code, res)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, blBase+"/"+uuid.NewString()+"/sync", ``, admin); rec.Code != http.StatusNotFound {
		t.Errorf("sync unknown = %d, want 404", rec.Code)
	}

	if rec := sendJSONAuth(srv, http.MethodDelete, blBase+"/"+created.ID, ``, admin); rec.Code != http.StatusNoContent {
		t.Errorf("delete = %d", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, blBase+"/"+created.ID, ``, admin); rec.Code != http.StatusNotFound {
		t.Errorf("re-delete = %d, want 404", rec.Code)
	}
}

func TestBlocklistSyncNowIsRateLimited(t *testing.T) {
	srv, _ := blServer(t)
	admin, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := sendJSONAuth(srv, http.MethodPost, blBase, `{"url":"https://lists.example/a.csv","format":"json"}`, admin)
	var sub blocklistSubscriptionView
	_ = json.Unmarshal(rec.Body.Bytes(), &sub)
	var last int
	for i := 0; i < blocklistSyncRateLimit+2; i++ {
		rec = sendJSONAuth(srv, http.MethodPost, blBase+"/"+sub.ID+"/sync", ``, admin)
		last = rec.Code
	}
	if last != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("after the budget: %d retry-after=%q, want 429 with Retry-After", last, rec.Header().Get("Retry-After"))
	}
}

func TestBlocklistAllowlist(t *testing.T) {
	srv, repo := blServer(t)
	admin, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	p := "/api/v1/admin/instances/blocklist-allowlist"
	if rec := sendJSONAuth(srv, http.MethodPost, p, `{"domain":"Friend.Example"}`, admin); rec.Code != http.StatusNoContent || !repo.allow["friend.example"] {
		t.Fatalf("allow = %d %v", rec.Code, repo.allow)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, p, `{"domain":"https://x"}`, admin); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad domain = %d, want 422", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, p, `{}`, admin); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing domain = %d, want 422", rec.Code)
	}
}

func (f *blFakeRepo) UpdateBlocklistSubscription(_ context.Context, a sqlcgen.UpdateBlocklistSubscriptionParams) (sqlcgen.BlocklistSubscription, error) {
	s, ok := f.subs[a.ID]
	if !ok {
		return s, pgx.ErrNoRows
	}
	s.Label, s.Enabled = a.Label, a.Enabled
	f.subs[a.ID] = s
	return s, nil
}
