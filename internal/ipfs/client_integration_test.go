//go:build ipfs_integration

// Integration tests for the Kubo RPC client against a REAL ipfs/kubo node. They
// are excluded from `make ci` and the default `-tags=integration` run by the
// dedicated `ipfs_integration` build tag, so a nodeless CI/dev box stays green —
// and even under `-tags ipfs_integration` each test self-skips when no node is
// configured. Bring up the compose `ipfs` profile (or a dedicated ipfs-test node)
// and point the test env at it:
//
//	docker compose --profile core --profile ipfs up -d ipfs
//	IPFS_TEST_API_URL=http://localhost:5001 \
//	IPFS_TEST_GATEWAY_URL=http://localhost:9090 \
//	  go test -tags ipfs_integration ./internal/ipfs/
//
// The optional CI job (.github/workflows/ipfs-integration.yml) runs a dedicated
// kubo service and sets these vars; the canonical backend-ci.yml gate is unchanged.
package ipfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// testClient returns a client for the real node (skipping when unconfigured) plus
// the gateway base (may be empty).
func testClient(t *testing.T) (*KuboClient, string) {
	t.Helper()
	api := os.Getenv("IPFS_TEST_API_URL")
	if api == "" {
		t.Skip("IPFS_TEST_API_URL not set; skipping real-kubo integration test")
	}
	return NewKuboClient(api, &http.Client{Timeout: 30 * time.Second}), strings.TrimRight(os.Getenv("IPFS_TEST_GATEWAY_URL"), "/")
}

// catViaRPC fetches a CID's bytes back through the node's /api/v0/cat (reusing the
// package-internal POST helper — the RPC API is POST-only).
func catViaRPC(t *testing.T, c *KuboClient, cid string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body, err := c.post(ctx, "/api/v0/cat?arg="+url.QueryEscape(cid), nil, "")
	if err != nil {
		t.Fatalf("cat %s: %v", cid, err)
	}
	defer body.Close()
	// The live fixture is capped at 16 MiB plus a short freshness marker.
	b, err := io.ReadAll(io.LimitReader(body, 17<<20))
	if err != nil {
		t.Fatalf("read cat body: %v", err)
	}
	return b
}

// getViaGateway fetches a gateway path and returns its body (bounded).
func getViaGateway(t *testing.T, gatewayURL, path string) []byte {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, gatewayURL+path, nil)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("gateway GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway GET %s = %d, want 200", path, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read gateway body: %v", err)
	}
	return b
}

// publicProofInput returns the independently hosted gateway and a small real
// video fixture. Local tagged runs self-skip when either is absent; CI sets the
// sentinel so a wiring regression cannot turn the live proof falsely green.
func publicProofInput(t *testing.T) (string, []byte) {
	t.Helper()
	gw := strings.TrimRight(os.Getenv("IPFS_TEST_PUBLIC_GATEWAY_URL"), "/")
	videoPath := os.Getenv("IPFS_TEST_VIDEO_PATH")
	if gw == "" || videoPath == "" {
		msg := "IPFS_TEST_PUBLIC_GATEWAY_URL and IPFS_TEST_VIDEO_PATH are required for the public-video proof"
		if os.Getenv("IPFS_PUBLIC_PROOFS_REQUIRED") != "" {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}
	info, err := os.Stat(videoPath)
	if err != nil {
		t.Fatalf("stat public-video fixture: %v", err)
	}
	if info.Size() == 0 || info.Size() > 16<<20 {
		t.Fatalf("public-video fixture size = %d; want 1..16 MiB", info.Size())
	}
	base, err := os.ReadFile(videoPath)
	if err != nil {
		t.Fatalf("read public-video fixture: %v", err)
	}
	// A unique trailing marker keeps every run's CID fresh, preventing a cached
	// response from an older run from masquerading as live provider reachability.
	// MP4 readers tolerate trailing application bytes; the source fixture remains
	// a real playable video rather than a text blob with a .mp4 name.
	data := append([]byte(nil), base...)
	data = append(data, []byte(fmt.Sprintf("\nvidra-public-ipfs-proof:%d\n", time.Now().UnixNano()))...)
	return gw, data
}

func getViaPublicGatewayEventually(t *testing.T, gatewayURL, cid string, want []byte) {
	t.Helper()
	timeout := 3 * time.Minute
	if raw := os.Getenv("IPFS_TEST_PUBLIC_GATEWAY_TIMEOUT"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			t.Fatalf("IPFS_TEST_PUBLIC_GATEWAY_TIMEOUT=%q is not a positive duration", raw)
		}
		timeout = parsed
	}

	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 35 * time.Second}
	path := gatewayURL + "/ipfs/" + cid
	last := "no request made"
	for {
		req, err := http.NewRequest(http.MethodGet, path, nil)
		if err != nil {
			t.Fatalf("build public gateway request: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			last = err.Error()
		} else {
			got, readErr := io.ReadAll(io.LimitReader(resp.Body, int64(len(want)+1)))
			_ = resp.Body.Close()
			switch {
			case readErr != nil:
				last = "read response: " + readErr.Error()
			case resp.StatusCode != http.StatusOK:
				last = fmt.Sprintf("status %d", resp.StatusCode)
			case !bytes.Equal(got, want):
				last = fmt.Sprintf("body mismatch (got %d bytes, want %d)", len(got), len(want))
			default:
				t.Logf("public gateway retrieved fresh video CID %s (%d bytes)", cid, len(got))
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("public gateway did not retrieve fresh CID %s within %s: %s", cid, timeout, last)
		}
		time.Sleep(3 * time.Second)
	}
}

// TestIntegrationAddPinCat: version probe, single-object add+pin, and a cat
// round-trip through the RPC and (when configured) the HTTP gateway.
func TestIntegrationAddPinCat(t *testing.T) {
	c, gw := testClient(t)
	ctx := context.Background()

	if _, err := c.Version(ctx); err != nil {
		t.Fatalf("Version: %v (is the node reachable at IPFS_TEST_API_URL?)", err)
	}

	// Unique payload so the test is repeatable against a persistent node.
	data := []byte("vidra ipfs integration " + time.Now().UTC().Format(time.RFC3339Nano))
	res, err := c.Add(ctx, "blob.txt", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := ValidateCID(res.CID); err != nil {
		t.Fatalf("node returned an invalid CID %q: %v", res.CID, err)
	}
	if res.Size == 0 {
		t.Error("Add reported zero size")
	}
	if pinned, err := c.IsPinned(ctx, res.CID); err != nil || !pinned {
		t.Fatalf("IsPinned = %v, %v; want true, nil", pinned, err)
	}
	if got := catViaRPC(t, c, res.CID); !bytes.Equal(got, data) {
		t.Errorf("cat mismatch: got %q, want %q", got, data)
	}
	if gw != "" {
		if got := getViaGateway(t, gw, "/ipfs/"+res.CID); !bytes.Equal(got, data) {
			t.Errorf("gateway mismatch: got %q, want %q", got, data)
		}
	}

	// Unpin cleans up (idempotent).
	if err := c.Unpin(ctx, res.CID); err != nil {
		t.Errorf("Unpin: %v", err)
	}
}

// TestIntegrationAddDirectoryHLSTree: directory-add a small HLS tree and resolve a
// nested relative path (720p/seg_00000.ts) under the single root CID through the
// gateway — the real proof that {gateway}/ipfs/{car_root}/… resolves unchanged.
func TestIntegrationAddDirectoryHLSTree(t *testing.T) {
	c, gw := testClient(t)
	ctx := context.Background()

	nonce := time.Now().UTC().Format(time.RFC3339Nano)
	seg := []byte("...ts-segment-bytes-" + nonce)
	entries := []DirEntry{
		{Path: "master.m3u8", Data: strings.NewReader("#EXTM3U\n" + nonce + "\n720p/playlist.m3u8\n")},
		{Path: "720p/playlist.m3u8", Data: strings.NewReader("#EXTM3U\nseg_00000.ts\n")},
		{Path: "720p/seg_00000.ts", Data: bytes.NewReader(seg)},
	}
	res, err := c.AddDirectory(ctx, entries)
	if err != nil {
		t.Fatalf("AddDirectory: %v", err)
	}
	if err := ValidateCID(res.CID); err != nil {
		t.Fatalf("directory root %q invalid: %v", res.CID, err)
	}
	if pinned, err := c.IsPinned(ctx, res.CID); err != nil || !pinned {
		t.Fatalf("directory root IsPinned = %v, %v; want true, nil", pinned, err)
	}

	if gw != "" {
		got := getViaGateway(t, gw, "/ipfs/"+res.CID+"/720p/seg_00000.ts")
		if !bytes.Equal(got, seg) {
			t.Errorf("gateway nested-path mismatch: got %q, want %q", got, seg)
		}
	} else {
		t.Log("IPFS_TEST_GATEWAY_URL unset; skipped the gateway nested-path resolution check")
	}

	if err := c.Unpin(ctx, res.CID); err != nil {
		t.Errorf("Unpin: %v", err)
	}
}

// TestIntegrationPublicVideoRoundTrip is the live-network proof: add+pin a
// fresh real MP4 on this node, verify its bytes locally, then retrieve the same
// CID through an independently operated public gateway. Local gateway success
// alone is insufficient because it does not prove DHT providing or reachability.
func TestIntegrationPublicVideoRoundTrip(t *testing.T) {
	c, _ := testClient(t)
	publicGateway, data := publicProofInput(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	res, err := c.Add(ctx, "vidra-public-proof.mp4", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Add public video: %v", err)
	}
	t.Cleanup(func() { _ = c.Unpin(context.Background(), res.CID) })
	if err := ValidateCID(res.CID); err != nil {
		t.Fatalf("public video CID %q invalid: %v", res.CID, err)
	}
	if got := catViaRPC(t, c, res.CID); !bytes.Equal(got, data) {
		t.Fatalf("local public-video cat mismatch: got %d bytes, want %d", len(got), len(data))
	}

	getViaPublicGatewayEventually(t, publicGateway, res.CID, data)
}
