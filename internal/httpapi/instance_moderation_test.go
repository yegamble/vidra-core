package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// instanceModFakeRepo is an in-memory instancemod.Repository.
type instanceModFakeRepo struct {
	mutes   map[string]time.Time // "<muter>|<domain>"
	blocked map[string]sqlcgen.BlockedInstance
}

func newInstanceModFakeRepo() *instanceModFakeRepo {
	return &instanceModFakeRepo{
		mutes:   map[string]time.Time{},
		blocked: map[string]sqlcgen.BlockedInstance{},
	}
}

func (f *instanceModFakeRepo) MuteInstance(_ context.Context, a sqlcgen.MuteInstanceParams) (int64, error) {
	k := a.MuterID.String() + "|" + a.Domain
	if _, ok := f.mutes[k]; ok {
		return 0, nil
	}
	f.mutes[k] = time.Now()
	return 1, nil
}

func (f *instanceModFakeRepo) UnmuteInstance(_ context.Context, a sqlcgen.UnmuteInstanceParams) (int64, error) {
	delete(f.mutes, a.MuterID.String()+"|"+a.Domain)
	return 1, nil
}

func (f *instanceModFakeRepo) ListMutedInstances(_ context.Context, a sqlcgen.ListMutedInstancesParams) ([]sqlcgen.ListMutedInstancesRow, error) {
	var rows []sqlcgen.ListMutedInstancesRow
	prefix := a.MuterID.String() + "|"
	for k, at := range f.mutes {
		if strings.HasPrefix(k, prefix) {
			rows = append(rows, sqlcgen.ListMutedInstancesRow{Domain: strings.TrimPrefix(k, prefix), CreatedAt: at})
		}
	}
	return rows, nil
}

func (f *instanceModFakeRepo) BlockInstance(_ context.Context, a sqlcgen.BlockInstanceParams) (int64, error) {
	f.blocked[a.Domain] = sqlcgen.BlockedInstance{
		Domain: a.Domain, Reason: a.Reason, BlockedBy: a.BlockedBy, CreatedAt: time.Now(),
	}
	return 1, nil
}

func (f *instanceModFakeRepo) UnblockInstance(_ context.Context, domain string) (int64, error) {
	delete(f.blocked, domain)
	return 1, nil
}

func (f *instanceModFakeRepo) ListBlockedInstances(_ context.Context, _ sqlcgen.ListBlockedInstancesParams) ([]sqlcgen.BlockedInstance, error) {
	var rows []sqlcgen.BlockedInstance
	for _, b := range f.blocked {
		rows = append(rows, b)
	}
	return rows, nil
}

func TestInstanceMutesRequireAuth(t *testing.T) {
	srv := videoServer(t)
	if rec := getWithAuth(srv, "/api/v1/me/mutes/instances", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon list = %d, want 401", rec.Code)
	}
	if rec := postTo(srv, "/api/v1/me/mutes/instances/spam.example", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon mute = %d, want 401", rec.Code)
	}
}

func TestInstanceMuteFlow(t *testing.T) {
	srv := videoServer(t)
	tok, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Initially empty.
	var list mutedInstanceListResponse
	rec := getWithAuth(srv, "/api/v1/me/mutes/instances", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d; body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Instances) != 0 {
		t.Fatalf("initial instances = %+v, want none", list.Instances)
	}

	// Mute (normalizes case), idempotent re-mute.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/mutes/instances/Spam.Example", "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("mute = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/mutes/instances/spam.example", "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("re-mute = %d", rec.Code)
	}
	rec = getWithAuth(srv, "/api/v1/me/mutes/instances", tok)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Instances) != 1 || list.Instances[0].Domain != "spam.example" {
		t.Fatalf("instances = %+v, want [spam.example]", list.Instances)
	}

	// Invalid domain → 422.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/mutes/instances/not%20a%20domain", "", tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid domain mute = %d, want 422", rec.Code)
	}

	// Unmute; idempotent.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/mutes/instances/spam.example", "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("unmute = %d", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/mutes/instances/spam.example", "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("re-unmute = %d", rec.Code)
	}
	rec = getWithAuth(srv, "/api/v1/me/mutes/instances", tok)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Instances) != 0 {
		t.Errorf("after unmute instances = %+v, want none", list.Instances)
	}
}

func TestBlockedInstancesAdminOnly(t *testing.T) {
	srv := videoServer(t)
	// The first registered account ("ada") becomes admin; bob stays a user.
	admin, _ := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	bob, _ := registerAndUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	if rec := getWithAuth(srv, "/api/v1/admin/instances/blocked", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon list = %d, want 401", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/instances/blocked", bob); rec.Code != http.StatusForbidden {
		t.Errorf("user list = %d, want 403", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/instances/blocked", `{"domain":"bad.example"}`, bob); rec.Code != http.StatusForbidden {
		t.Errorf("user block = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/instances/blocked", admin); rec.Code != http.StatusOK {
		t.Errorf("admin list = %d, want 200", rec.Code)
	}
}

func TestBlockInstanceFlow(t *testing.T) {
	srv := videoServer(t)
	admin, adminID := registerAndUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Block with a reason; re-block idempotently refreshes it.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/instances/blocked", `{"domain":"Bad.Example","reason":"spam wave"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/instances/blocked", `{"domain":"bad.example","reason":"still spam"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("re-block = %d", rec.Code)
	}

	var list blockedInstanceListResponse
	rec := getWithAuth(srv, "/api/v1/admin/instances/blocked", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Instances) != 1 {
		t.Fatalf("instances = %+v, want 1", list.Instances)
	}
	got := list.Instances[0]
	if got.Domain != "bad.example" || got.Reason != "still spam" || got.BlockedBy != adminID {
		t.Errorf("blocked entry = %+v", got)
	}

	// Validation: missing domain and invalid domain → 422.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/instances/blocked", `{"reason":"x"}`, admin); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing domain = %d, want 422", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/instances/blocked", `{"domain":"https://bad.example"}`, admin); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("URL as domain = %d, want 422", rec.Code)
	}

	// Unblock; idempotent.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/instances/blocked/bad.example", "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock = %d", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/instances/blocked/bad.example", "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("re-unblock = %d", rec.Code)
	}
	rec = getWithAuth(srv, "/api/v1/admin/instances/blocked", admin)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Instances) != 0 {
		t.Errorf("after unblock instances = %+v, want none", list.Instances)
	}
}
