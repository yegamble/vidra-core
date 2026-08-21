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
