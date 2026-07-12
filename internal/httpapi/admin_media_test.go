package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

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
