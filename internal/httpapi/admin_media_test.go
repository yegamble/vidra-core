package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/mediagc"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// mediagcFakeRepo returns empty reference sets, so any stored object under a
// known prefix is an orphan. It is wired into the shared test server.
type mediagcFakeRepo struct{}

func (mediagcFakeRepo) ListAllVideoFileKeys(context.Context) ([]string, error) { return nil, nil }
func (mediagcFakeRepo) ListAllCaptionKeys(context.Context) ([]string, error)   { return nil, nil }
func (mediagcFakeRepo) ListAllVideoIDs(context.Context) ([]uuid.UUID, error)   { return nil, nil }
func (mediagcFakeRepo) ListPlaylistThumbnailRefs(context.Context) ([]sqlcgen.ListPlaylistThumbnailRefsRow, error) {
	return nil, nil
}
func (mediagcFakeRepo) ListStreamingPlaylistRefs(context.Context) ([]sqlcgen.ListStreamingPlaylistRefsRow, error) {
	return nil, nil
}

func TestAdminMediaGC(t *testing.T) {
	srv, blobs, _, _ := videoServerEnv(t, testConfig())
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Seed an orphan blob under a swept prefix (empty reference set → orphan).
	orphan := "web-videos/orphan.mp4"
	if _, err := blobs.Put(context.Background(), orphan, strings.NewReader("bytes")); err != nil {
		t.Fatal(err)
	}

	// Non-admin → 403.
	if rec := postJSONAuth(srv, "/api/v1/admin/media/gc", `{}`, bobTok); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin gc = %d, want 403", rec.Code)
	}
	// Anonymous → 401.
	if rec := postJSONAuth(srv, "/api/v1/admin/media/gc", `{}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon gc = %d, want 401", rec.Code)
	}

	// Default (no body / empty) is a dry run: reports the orphan, deletes nothing.
	rec := postJSONAuth(srv, "/api/v1/admin/media/gc", `{}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry-run gc = %d; body=%s", rec.Code, rec.Body.String())
	}
	var res mediaGCResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if !res.DryRun || res.Deleted != 0 {
		t.Fatalf("dry-run response = %+v, want DryRun=true Deleted=0", res)
	}
	if len(res.Orphans) != 1 || res.Orphans[0] != orphan {
		t.Fatalf("dry-run orphans = %v, want [%s]", res.Orphans, orphan)
	}
	if ok, _ := blobs.Exists(context.Background(), orphan); !ok {
		t.Fatal("dry run deleted the orphan")
	}

	// dry_run=false deletes the orphan.
	rec = postJSONAuth(srv, "/api/v1/admin/media/gc", `{"dry_run":false}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete gc = %d; body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res.DryRun || res.Deleted != 1 {
		t.Fatalf("delete response = %+v, want DryRun=false Deleted=1", res)
	}
	if ok, _ := blobs.Exists(context.Background(), orphan); ok {
		t.Error("orphan survived a delete sweep")
	}
}

// The GC response is the operator's only view of why a sweep they asked to
// delete deleted nothing, so the safety fields have to arrive with it.
func TestAdminMediaGCReportsTheSafetyFields(t *testing.T) {
	srv, blobs, _, _ := videoServerEnv(t, testConfig())
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	orphan := "web-videos/orphan.mp4"
	if _, err := blobs.Put(context.Background(), orphan, strings.NewReader("bytes")); err != nil {
		t.Fatal(err)
	}

	// Local storage: ownership does not apply, and a dry run says so.
	rec := postJSONAuth(srv, "/api/v1/admin/media/gc", `{}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("gc = %d; body=%s", rec.Code, rec.Body.String())
	}
	var res mediaGCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Mode != mediagc.ModeDryRun || res.BucketOwnership != string(mediagc.OwnershipNotApplicable) {
		t.Fatalf("mode/ownership = %q/%q, want %q/%q", res.Mode, res.BucketOwnership, mediagc.ModeDryRun, mediagc.OwnershipNotApplicable)
	}
	if res.OrphanPercent != 100 || res.BreakerTripped || res.ForcedDryRun {
		t.Errorf("dry-run response = %+v, want orphan_percent=100 and neither rail engaged", res)
	}

	// An unowned store downgrades a requested delete to a dry run, with a full
	// orphan list and the reason attached — 200, not an error.
	srv.mediagcsvc = mediagc.NewService(&mediagcFakeRepo{}, blobs,
		mediagc.WithBucketOwnership(mediagc.OwnershipUnowned))
	rec = postJSONAuth(srv, "/api/v1/admin/media/gc", `{"dry_run":false}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("gc on an unowned bucket = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.ForcedDryRun || !res.DryRun || res.Mode != mediagc.ModeDryRun || res.Deleted != 0 {
		t.Fatalf("unowned response = %+v, want a forced dry run that deleted nothing", res)
	}
	if len(res.Orphans) != 1 || res.Orphans[0] != orphan {
		t.Errorf("orphans = %v, want the full list even when nothing was deleted", res.Orphans)
	}
	if ok, _ := blobs.Exists(context.Background(), orphan); !ok {
		t.Error("a sweep of an unowned bucket deleted the orphan")
	}
}

// MEDIA_GC_ENABLED and MEDIA_GC_MAX_ORPHAN_PERCENT are deliberately boot-baked
// (a runtime override would let a settings mistake become an irreversible one),
// but boot-baked must not mean invisible: before this GET, an admin could not
// see whether the daily DESTRUCTIVE sweep is on, its breaker limit, or who owns
// the bucket, without running a manual dry run to find out.
func TestAdminMediaGCBootFacts(t *testing.T) {
	cfg := testConfig()
	cfg.MediaGCEnabled = true
	cfg.MediaGCMaxOrphanPercent = 25
	srv, blobs, _, _ := videoServerEnv(t, cfg)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	const path = "/api/v1/admin/media/gc"
	if rec := getWithAuth(srv, path, bobTok); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin gc facts = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, path, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon gc facts = %d, want 401", rec.Code)
	}

	read := func() mediaGCConfigResponse {
		t.Helper()
		rec := getWithAuth(srv, path, adminTok)
		if rec.Code != http.StatusOK {
			t.Fatalf("gc facts = %d; body=%s", rec.Code, rec.Body.String())
		}
		var res mediaGCConfigResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		return res
	}

	res := read()
	if !res.Enabled || res.MaxOrphanPercent != 25 {
		t.Errorf("gc facts = %+v, want enabled with the 25%% breaker reported", res)
	}
	// Local storage: ownership does not apply, and the page says so with the
	// same vocabulary the sweep result uses.
	if res.BucketOwnership != string(mediagc.OwnershipNotApplicable) {
		t.Errorf("bucket_ownership = %q, want %q", res.BucketOwnership, mediagc.OwnershipNotApplicable)
	}

	// Ownership is the service's CURRENT in-memory state, not a boot snapshot:
	// an adoption (or a conflicting marker found by a sweep) must show on the
	// next read.
	srv.mediagcsvc = mediagc.NewService(&mediagcFakeRepo{}, blobs,
		mediagc.WithBucketOwnership(mediagc.OwnershipUnowned))
	if res := read(); res.BucketOwnership != string(mediagc.OwnershipUnowned) {
		t.Errorf("bucket_ownership = %q after re-wiring, want %q", res.BucketOwnership, mediagc.OwnershipUnowned)
	}
}

// The adoption endpoint is the deliberate way out of an unowned bucket: admin
// only, audited, and it must actually re-enable deletion.
func TestAdminAdoptBucket(t *testing.T) {
	srv, blobs, _, _ := videoServerEnv(t, testConfig())
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	const path = "/api/v1/admin/media/gc/adopt-bucket"
	if rec := postJSONAuth(srv, path, `{}`, bobTok); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin adopt = %d, want 403", rec.Code)
	}
	if rec := postJSONAuth(srv, path, `{}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon adopt = %d, want 401", rec.Code)
	}

	// Local storage has no bucket to adopt, and says so rather than pretending.
	if rec := postJSONAuth(srv, path, `{}`, adminTok); rec.Code != http.StatusConflict {
		t.Fatalf("adopt on local storage = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}

	// Without an instance identity there is nothing to stamp.
	srv.mediagcsvc = mediagc.NewService(&mediagcFakeRepo{}, blobs,
		mediagc.WithBucketOwnership(mediagc.OwnershipUnowned))
	if rec := postJSONAuth(srv, path, `{}`, adminTok); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("adopt without an identity = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}

	// The real path: adopt, then a requested delete actually deletes.
	const identity = "55555555-5555-4555-8555-555555555555"
	srv.mediagcsvc = mediagc.NewService(&mediagcFakeRepo{}, blobs,
		mediagc.WithBucketOwnership(mediagc.OwnershipUnowned),
		mediagc.WithInstanceIdentity(identity))
	orphan := "web-videos/orphan.mp4"
	if _, err := blobs.Put(context.Background(), orphan, strings.NewReader("bytes")); err != nil {
		t.Fatal(err)
	}

	rec := postJSONAuth(srv, path, `{}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("adopt = %d; body=%s", rec.Code, rec.Body.String())
	}
	var adopted adoptBucketResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &adopted); err != nil {
		t.Fatal(err)
	}
	if adopted.BucketOwnership != string(mediagc.OwnershipOwned) || adopted.MarkerKey != storage.OwnerMarkerKey {
		t.Fatalf("adopt response = %+v, want owned at %q", adopted, storage.OwnerMarkerKey)
	}
	marker, found, err := storage.ReadOwnerMarker(context.Background(), blobs)
	if err != nil || !found || marker != identity {
		t.Fatalf("marker after adoption = (%q, %v, %v), want %q", marker, found, err, identity)
	}

	rec = postJSONAuth(srv, "/api/v1/admin/media/gc", `{"dry_run":false}`, adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("gc after adoption = %d; body=%s", rec.Code, rec.Body.String())
	}
	var res mediaGCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.ForcedDryRun || res.Mode != mediagc.ModeDelete || res.Deleted != 1 {
		t.Fatalf("gc after adoption = %+v, want a real delete", res)
	}
	if ok, _ := blobs.Exists(context.Background(), orphan); ok {
		t.Error("the orphan survived a sweep of an adopted bucket")
	}
}
