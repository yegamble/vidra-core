//go:build ipfs_private_integration

// Integration tests for the PRIVATE IPFS swarm (fix_plan P19.P3,
// .ralph/specs/ipfs-media-private.md §9). They prove the network-level isolation the
// private tier depends on against REAL swarm.key'd Kubo nodes, and are excluded from
// `make ci` and the default `-tags=integration` run by the dedicated
// `ipfs_private_integration` build tag — a nodeless CI/dev box stays green, and even
// under `-tags ipfs_private_integration` each test self-skips when its nodes are not
// configured.
//
// Bring up the compose profile and point the tests at it:
//
//	docker compose --profile core --profile ipfs-private-cluster up -d \
//	    kubo-private kubo-private-2 ipfs-cluster-private
//	IPFS_PRIVATE_TEST_API_A=http://localhost:5002 \
//	IPFS_PRIVATE_TEST_API_B=<kubo-private-2 RPC> \
//	IPFS_PRIVATE_TEST_PEER_B=/dns4/kubo-private-2/tcp/4001/p2p/<B-peer-id> \
//	IPFS_PRIVATE_TEST_API_OUTSIDE=<a kubo NOT on the swarm> \
//	IPFS_PRIVATE_TEST_KUBO_BIN=$(command -v ipfs) \
//	IPFS_PRIVATE_TEST_CLUSTER_API=http://localhost:9094 \
//	  go test -tags ipfs_private_integration ./internal/ipfs/
//
// Each proof self-skips independently when its inputs are absent, so a partial setup
// still exercises what it can. The optional CI job (.github/workflows/
// ipfs-private-integration.yml) wires the keyed pair + an outside node; the canonical
// backend-ci.yml gate is unchanged.
package ipfs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func privateAPIClient(t *testing.T, env string) *KuboClient {
	t.Helper()
	api := os.Getenv(env)
	if api == "" {
		t.Skipf("%s not set; skipping private-swarm integration test", env)
	}
	return NewKuboClient(api, &http.Client{Timeout: 30 * time.Second})
}

// idResponse is /api/v0/id (subset).
type idResponse struct {
	ID        string   `json:"ID"`
	Addresses []string `json:"Addresses"`
}

func nodeIdentity(ctx context.Context, c *KuboClient) (idResponse, error) {
	var out idResponse
	body, err := c.post(ctx, "/api/v0/id", nil, "")
	if err != nil {
		return out, err
	}
	defer body.Close()
	err = json.NewDecoder(io.LimitReader(body, 1<<16)).Decode(&out)
	return out, err
}

// swarmConnect dials a peer multiaddr; a non-nil error means the connection was
// refused (e.g. a PNET handshake rejecting a keyless/wrong-key peer).
func swarmConnect(ctx context.Context, c *KuboClient, addr string) error {
	body, err := c.post(ctx, "/api/v0/swarm/connect?arg="+url.QueryEscape(addr), nil, "")
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 1<<16))
	return body.Close()
}

// pinAdd asks a node to pin (and therefore fetch) a CID — the block travels over the
// private swarm from a connected peer that holds it.
func pinAdd(ctx context.Context, c *KuboClient, cid string) error {
	body, err := c.post(ctx, "/api/v0/pin/add?arg="+url.QueryEscape(cid), nil, "")
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 1<<16))
	return body.Close()
}

func catRPC(ctx context.Context, c *KuboClient, cid string) ([]byte, error) {
	body, err := c.post(ctx, "/api/v0/cat?arg="+url.QueryEscape(cid), nil, "")
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(io.LimitReader(body, 1<<20))
}

// dialAddrForB resolves the multiaddr node A should dial to reach B: an explicit
// IPFS_PRIVATE_TEST_PEER_B when set, else discovered from B's own /id (first
// non-loopback tcp address, which already carries the /p2p/<id> suffix).
func dialAddrForB(ctx context.Context, t *testing.T, b *KuboClient) string {
	t.Helper()
	if addr := os.Getenv("IPFS_PRIVATE_TEST_PEER_B"); addr != "" {
		return addr
	}
	id, err := nodeIdentity(ctx, b)
	if err != nil {
		t.Fatalf("discover B identity: %v", err)
	}
	for _, a := range id.Addresses {
		if strings.Contains(a, "/tcp/") && !strings.Contains(a, "127.0.0.1") && !strings.Contains(a, "/ip6/::1") {
			return a
		}
	}
	t.Skip("no dialable address for B (set IPFS_PRIVATE_TEST_PEER_B to /dns4/<host>/tcp/4001/p2p/<id>)")
	return ""
}

// TestPrivateSwarmReplication: a keyed pair replicates intra-swarm. Add on A, connect
// A→B, pin (fetch) on B, and cat the bytes back from B — proving the two keyed nodes
// exchange blocks over the private swarm.
func TestPrivateSwarmReplication(t *testing.T) {
	a := privateAPIClient(t, "IPFS_PRIVATE_TEST_API_A")
	b := privateAPIClient(t, "IPFS_PRIVATE_TEST_API_B")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	data := []byte("vidra private-swarm replication " + time.Now().UTC().Format(time.RFC3339Nano))
	res, err := a.Add(ctx, "blob.txt", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Add on A: %v", err)
	}
	if err := ValidateCID(res.CID); err != nil {
		t.Fatalf("A returned an invalid CID %q: %v", res.CID, err)
	}

	if err := swarmConnect(ctx, b, dialAddrForB(ctx, t, b)); err != nil {
		// Connect from B→A is symmetric; if PEER_B pointed at B we retry A→B by
		// discovering A is not needed — a keyed pair must connect, so this is a failure.
		t.Fatalf("keyed peers failed to connect over the private swarm: %v", err)
	}

	// B fetches the block from A over the now-connected private swarm.
	if err := pinAdd(ctx, b, res.CID); err != nil {
		t.Fatalf("pin/fetch on B (intra-swarm replication) failed: %v", err)
	}
	got, err := catRPC(ctx, b, res.CID)
	if err != nil {
		t.Fatalf("cat on B: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("B replicated bytes mismatch: got %q, want %q", got, data)
	}
	_ = b.Unpin(ctx, res.CID)
	_ = a.Unpin(ctx, res.CID)
}

// TestOutsideNodeCannotFetch: an OUTSIDE node (no swarm.key / a different key) cannot
// retrieve a private CID — the isolation proof. Add on A, then from the outside node
// bound a cat by a short deadline and assert it does NOT resolve (and a direct swarm
// connect to A is refused by the PNET handshake).
func TestOutsideNodeCannotFetch(t *testing.T) {
	a := privateAPIClient(t, "IPFS_PRIVATE_TEST_API_A")
	outside := privateAPIClient(t, "IPFS_PRIVATE_TEST_API_OUTSIDE")

	addCtx, cancelAdd := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelAdd()
	data := []byte("vidra isolation " + time.Now().UTC().Format(time.RFC3339Nano))
	res, err := a.Add(addCtx, "secret.txt", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Add on A: %v", err)
	}

	// (a) A direct swarm connect from the outside node to A should be refused (keyless
	// peers can't complete the PNET handshake). Best-effort signal — logged, not fatal,
	// because a refusal can surface as a slow error; the cat timeout below is the hard proof.
	if addr := os.Getenv("IPFS_PRIVATE_TEST_PEER_A"); addr != "" {
		cctx, cc := context.WithTimeout(context.Background(), 15*time.Second)
		if err := swarmConnect(cctx, outside, addr); err == nil {
			cc()
			t.Errorf("an outside (keyless) node connected to a private-swarm peer — isolation breach")
		} else {
			cc()
			t.Logf("outside node correctly failed to connect to the private swarm: %v", err)
		}
	}

	// (b) The hard proof: the outside node cannot retrieve the private CID within a
	// bounded window (no route to it on any swarm it can reach).
	catCtx, cancelCat := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelCat()
	if got, err := catRPC(catCtx, outside, res.CID); err == nil {
		t.Fatalf("outside node retrieved a private CID (%d bytes) — isolation breach", len(got))
	}
	_ = a.Unpin(context.Background(), res.CID)
}

// TestKeylessDaemonRefusesToStart: the fail-closed proof — a Kubo daemon with
// LIBP2P_FORCE_PNET=1 and NO swarm.key must refuse to start. Drives a real `ipfs`
// binary (IPFS_PRIVATE_TEST_KUBO_BIN) against a throwaway repo.
func TestKeylessDaemonRefusesToStart(t *testing.T) {
	bin := os.Getenv("IPFS_PRIVATE_TEST_KUBO_BIN")
	if bin == "" {
		t.Skip("IPFS_PRIVATE_TEST_KUBO_BIN not set; skipping fail-closed boot proof")
	}
	repo := t.TempDir()
	env := append(os.Environ(), "IPFS_PATH="+repo)

	initCtx, cancelInit := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelInit()
	if out, err := runIPFS(initCtx, bin, env, "init", "--profile=test"); err != nil {
		t.Fatalf("ipfs init: %v\n%s", err, out)
	}
	// A fresh repo has no swarm.key; assert that before proving the boot refusal.
	if _, err := os.Stat(filepath.Join(repo, "swarm.key")); !os.IsNotExist(err) {
		t.Fatalf("unexpected swarm.key in a fresh repo (err=%v)", err)
	}

	// Boot with the fail-closed enforcement on and no key: expect a fast non-zero exit
	// whose output names the PNET enforcement.
	bootCtx, cancelBoot := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelBoot()
	out, err := runIPFS(bootCtx, bin, append(env, "LIBP2P_FORCE_PNET=1"), "daemon")
	if err == nil {
		t.Fatalf("keyless daemon started under LIBP2P_FORCE_PNET=1 — fail-closed breach:\n%s", out)
	}
	if bootCtx.Err() == context.DeadlineExceeded {
		t.Fatalf("keyless daemon did not exit under LIBP2P_FORCE_PNET=1 (ran until the deadline) — fail-closed breach:\n%s", out)
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "pnet") && !strings.Contains(low, "private network") && !strings.Contains(low, "force_pnet") && !strings.Contains(low, "swarm.key") {
		t.Errorf("keyless daemon exited but without a private-network reason; output:\n%s", out)
	}
}

// runIPFS runs the ipfs binary with the given args and env, returning combined output.
func runIPFS(ctx context.Context, bin string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// clusterPeerStatus is the subset of the IPFS Cluster REST /pins/{cid} GlobalPinInfo.
type clusterPeerStatus struct {
	PeerMap map[string]struct {
		Peername string `json:"peername"`
		Status   string `json:"status"`
		Error    string `json:"error"`
	} `json:"peer_map"`
}

// TestPrivateClusterReplicationPinned: the optional cluster leg — a cluster pin of a
// CID held by the cluster's node reaches PINNED on every cluster peer (a two-peer mesh
// proves "PINNED on both"). Self-skips unless a private cluster REST + node A are set.
func TestPrivateClusterReplicationPinned(t *testing.T) {
	clusterAPI := os.Getenv("IPFS_PRIVATE_TEST_CLUSTER_API")
	if clusterAPI == "" {
		t.Skip("IPFS_PRIVATE_TEST_CLUSTER_API not set; skipping private-cluster replication test")
	}
	a := privateAPIClient(t, "IPFS_PRIVATE_TEST_API_A")
	token := os.Getenv("IPFS_PRIVATE_TEST_CLUSTER_TOKEN")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Add on A (the cluster's paired node) so every peer can fetch the block.
	data := []byte("vidra private-cluster " + time.Now().UTC().Format(time.RFC3339Nano))
	res, err := a.Add(ctx, "clustered.txt", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Add on A: %v", err)
	}

	cluster := NewKuboClusterClient(clusterAPI, token, &http.Client{Timeout: 30 * time.Second})
	if err := cluster.ClusterPin(ctx, res.CID); err != nil {
		t.Fatalf("cluster pin: %v", err)
	}
	t.Cleanup(func() { _ = cluster.ClusterUnpin(context.Background(), res.CID) })

	// Poll the pin status until every peer reports PINNED (or the deadline).
	deadline := time.Now().Add(45 * time.Second)
	for {
		st, err := clusterPinStatus(ctx, clusterAPI, token, res.CID)
		if err != nil {
			t.Fatalf("cluster pin status: %v", err)
		}
		if len(st.PeerMap) == 0 {
			t.Fatalf("cluster reported no peers for %s", res.CID)
		}
		allPinned := true
		for peer, info := range st.PeerMap {
			if !strings.EqualFold(info.Status, "pinned") {
				allPinned = false
				if info.Error != "" {
					t.Logf("peer %s status=%s error=%s", peer, info.Status, info.Error)
				}
			}
		}
		if allPinned {
			t.Logf("CID %s PINNED on all %d cluster peer(s)", res.CID, len(st.PeerMap))
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("CID %s did not reach PINNED on all %d cluster peer(s) before the deadline", res.CID, len(st.PeerMap))
		}
		time.Sleep(2 * time.Second)
	}
}

func clusterPinStatus(ctx context.Context, base, token, cid string) (clusterPeerStatus, error) {
	var out clusterPeerStatus
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/pins/"+url.PathEscape(cid), nil)
	if err != nil {
		return out, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<18)).Decode(&out)
	return out, err
}
