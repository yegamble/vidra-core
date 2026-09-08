//go:build ipfs_integration

// Integration proof for the MIRROR's directory pin against a REAL ipfs/kubo node.
//
// internal/ipfs's TestIntegrationAddDirectoryHLSTree proves the RPC contract —
// hand AddDirectory a set of relative paths and the gateway resolves them under
// one root. It cannot prove the thing that actually broke, because it is handed
// the entries: the mirror's job is to DERIVE them from the object store, and the
// core#199 regression was entirely in that derivation (it wrapped the stable
// per-video prefix, so the root held rN/ directories and no master.m3u8, and the
// pin still succeeded with a valid CID). This test drives the real worker over a
// real store layout and fetches master.m3u8 back through the node's gateway,
// which is exactly the request vidra-user's ipfs-backed lane makes.
//
// Same contract as the internal/ipfs proofs: excluded from `make ci` by the
// ipfs_integration build tag, self-skips when no node is configured.
//
//	docker compose --profile ipfs up -d ipfs
//	IPFS_TEST_API_URL=http://localhost:5001 \
//	IPFS_TEST_GATEWAY_URL=http://localhost:9090 \
//	  go test -tags ipfs_integration ./internal/ipfsmirror/

package ipfsmirror

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/media"
)

// TestIntegrationMirrorPinsPromotedGeneration: a video whose promoted transcode
// generation is r2 — with a superseded r1 still in the object store, as it is
// between promotion and the next mediagc sweep — must be published as a tree
// whose ROOT is the playable one: {gateway}/ipfs/{car_root}/master.m3u8 serves
// the promoted master, and the superseded generation is nowhere in it.
func TestIntegrationMirrorPinsPromotedGeneration(t *testing.T) {
	api := os.Getenv("IPFS_TEST_API_URL")
	if api == "" {
		t.Skip("IPFS_TEST_API_URL not set; skipping real-kubo integration test")
	}
	gateway := strings.TrimRight(os.Getenv("IPFS_TEST_GATEWAY_URL"), "/")
	if gateway == "" {
		t.Skip("IPFS_TEST_GATEWAY_URL not set; skipping real-kubo integration test")
	}
	client := ipfs.NewKuboClient(api, &http.Client{Timeout: 30 * time.Second})
	ctx := context.Background()

	repo := newFakeRepo()
	blobs := newBlobs(t)
	vid := uuid.New()
	stable := media.HLSKeyPrefix(vid) + "/"
	superseded := media.HLSPrefixForGeneration(vid, 1)
	promoted := media.HLSPrefixForGeneration(vid, 2)

	// A nonce keeps every run's bytes (and therefore every run's CID) distinct, so
	// a pass can never be a leftover pin from a previous run on the same node.
	nonce := time.Now().UTC().Format(time.RFC3339Nano)
	master := "#EXTM3U\n# promoted " + nonce + "\n720p/playlist.m3u8\n"
	putBlob(t, blobs, superseded+"/master.m3u8", "#EXTM3U\n# SUPERSEDED "+nonce+"\n")
	putBlob(t, blobs, superseded+"/720p/seg_00000.ts", "superseded-seg-"+nonce)
	putBlob(t, blobs, promoted+"/master.m3u8", master)
	putBlob(t, blobs, promoted+"/720p/playlist.m3u8", "#EXTM3U\nseg_00000.ts\n")
	putBlob(t, blobs, promoted+"/720p/seg_00000.ts", "promoted-seg-"+nonce)
	putBlob(t, blobs, promoted+"/"+media.VP9WebMFilename, "webm-alt-"+nonce)

	seedPendingVideo(repo, stable, string(ClassHLS), vid)
	svc := New(repo, &fakeLookups{hlsTree: promoted}, blobs, client, testConfig())

	if n, err := svc.DrainDue(ctx, 10); err != nil || n != 1 {
		t.Fatalf("DrainDue = %d, %v; want 1, nil (%s)", n, err, repo.rows[stable].LastError)
	}
	root := repo.rows[stable].CarRoot
	if root == "" {
		t.Fatalf("no car_root recorded (state %q: %s)", repo.state(stable), repo.rows[stable].LastError)
	}
	t.Cleanup(func() { _ = client.Unpin(context.Background(), root) })
	if err := ipfs.ValidateCID(root); err != nil {
		t.Fatalf("car_root %q invalid: %v", root, err)
	}

	// THE assertion: the request vidra-user's player makes first. Before the fix
	// this 404'd while everything else looked healthy.
	if got := getViaGateway(t, gateway, "/ipfs/"+root+"/master.m3u8"); got != master {
		t.Errorf("gateway master.m3u8 = %q, want %q", got, master)
	}
	if got := getViaGateway(t, gateway, "/ipfs/"+root+"/720p/seg_00000.ts"); got != "promoted-seg-"+nonce {
		t.Errorf("gateway nested segment = %q, want the promoted generation's", got)
	}
	// The superseded generation is not carried along, and neither is the VP9/WebM
	// alternate that shares the promoted directory (its own media class).
	for _, rel := range []string{"/r1/master.m3u8", "/r2/master.m3u8", "/" + media.VP9WebMFilename} {
		if code := statusViaGateway(t, gateway, "/ipfs/"+root+rel); code == http.StatusOK {
			t.Errorf("gateway served %s from the HLS car_root, want absent", rel)
		}
	}
}

func getViaGateway(t *testing.T, gateway, p string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gateway+p, nil)
	if err != nil {
		t.Fatalf("new request %s: %v", p, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("gateway GET %s: %v", p, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read gateway body %s: %v", p, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway GET %s = %d, want 200 (body %q)", p, resp.StatusCode, string(body))
	}
	return string(body)
}

func statusViaGateway(t *testing.T, gateway, p string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gateway+p, nil)
	if err != nil {
		t.Fatalf("new request %s: %v", p, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// A gateway that refuses to answer for a path the tree does not contain is
		// the outcome this check wants; only a 200 would be a failure.
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode
}
