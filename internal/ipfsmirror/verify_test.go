package ipfsmirror

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ---- the verifyReader/ledgerHealthReader half of the in-memory ledger --------
//
// These live on *fakeRepo so every existing test keeps its repository and the
// new sweeps see the same rows the drain does. They implement the SQL the real
// queries express — the keyset page, the DISTINCT live-CID set, the guarded
// re-arm — closely enough that a behaviour change in either is a test failure
// here, while the SQL itself stays the integration suite's business.

func (r *fakeRepo) ListIPFSPinsForVerify(_ context.Context, arg sqlcgen.ListIPFSPinsForVerifyParams) ([]sqlcgen.ListIPFSPinsForVerifyRow, error) {
	var keys []string
	for key, row := range r.rows {
		if normalizeNetwork(row.Network) == arg.Network && row.State == "pinned" && row.Cid != "" && key > arg.AfterObjectKey {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if int(arg.BatchSize) < len(keys) {
		keys = keys[:arg.BatchSize]
	}
	out := make([]sqlcgen.ListIPFSPinsForVerifyRow, 0, len(keys))
	for _, key := range keys {
		row := r.rows[key]
		out = append(out, sqlcgen.ListIPFSPinsForVerifyRow{
			ObjectKey: key, MediaClass: row.MediaClass, Cid: row.Cid,
			Network: normalizeNetwork(row.Network), VideoID: row.VideoID,
		})
	}
	return out, nil
}

func (r *fakeRepo) ListLiveIPFSPinCIDs(_ context.Context, arg sqlcgen.ListLiveIPFSPinCIDsParams) ([]string, error) {
	seen := map[string]bool{}
	for _, row := range r.rows {
		if normalizeNetwork(row.Network) != arg.Network || row.Cid == "" {
			continue
		}
		switch row.State {
		case "pinned", "pending", "unpinning":
			seen[row.Cid] = true
		}
	}
	out := make([]string, 0, len(seen))
	for cid := range seen {
		out = append(out, cid)
	}
	sort.Strings(out)
	if int(arg.MaxRows) < len(out) {
		out = out[:arg.MaxRows]
	}
	return out, nil
}

func (r *fakeRepo) RearmLostIPFSPin(_ context.Context, arg sqlcgen.RearmLostIPFSPinParams) (int64, error) {
	row, ok := r.rows[arg.ObjectKey]
	// The guard is the point: a row that moved under the sweep matches nothing.
	if !ok || row.State != "pinned" || row.Cid != arg.Cid {
		return 0, nil
	}
	row.State = "pending"
	row.Attempts = 0
	row.LastError = ""
	return 1, nil
}

func (r *fakeRepo) IPFSPinLedgerHealth(_ context.Context, network string) (sqlcgen.IPFSPinLedgerHealthRow, error) {
	var out sqlcgen.IPFSPinLedgerHealthRow
	for _, row := range r.rows {
		if normalizeNetwork(row.Network) != network {
			continue
		}
		switch row.State {
		case "pending", "unpinning":
			out.Backlog++
		case "failed":
			out.DeadLettered++
		case "pinned":
			out.Pinned++
		}
	}
	return out, nil
}

// seedPinnedOnNode puts one already-pinned public row in the ledger with a real CID.
func seedPinnedOnNode(t *testing.T, repo *fakeRepo, client *ipfs.FakeIPFSClient, key, content string) string {
	t.Helper()
	res, err := client.Add(context.Background(), key, strings.NewReader(content))
	if err != nil {
		t.Fatalf("fake Add: %v", err)
	}
	repo.rows[key] = &sqlcgen.MediaIpfsPin{
		ObjectKey: key, MediaClass: string(ClassThumbnail), Cid: res.CID,
		State: "pinned", Network: networkPublic,
	}
	return res.CID
}

// DIRECTION 1 — the ledger says pinned, the node does not hold it. A31 staged
// exactly this by hand (`ipfs pin rm`) and measured "nothing".
func TestVerifyPinsRearmsAPinTheNodeLost(t *testing.T) {
	repo, client := newFakeRepo(), ipfs.NewFakeIPFSClient()
	svc := New(repo, &fakeLookups{}, newBlobs(t), client, testConfig())

	kept := seedPinnedOnNode(t, repo, client, "thumbnails/kept.jpg", "kept-bytes")
	lost := seedPinnedOnNode(t, repo, client, "thumbnails/lost.jpg", "lost-bytes")
	client.DropPin(lost)

	results := svc.VerifyPins(context.Background())
	if len(results) != 1 || results[0].Network != networkPublic {
		t.Fatalf("results = %+v, want one public result", results)
	}
	if results[0].Rearmed != 1 || results[0].Checked != 2 {
		t.Fatalf("checked=%d rearmed=%d, want 2 and 1", results[0].Checked, results[0].Rearmed)
	}
	if got := repo.state("thumbnails/lost.jpg"); got != "pending" {
		t.Errorf("the lost pin's row is %q, want pending so the worker re-adds it", got)
	}
	if got := repo.state("thumbnails/kept.jpg"); got != "pinned" {
		t.Errorf("the intact pin's row is %q, want it untouched", got)
	}
	// The CID is preserved so the worker's swap logic no-ops on the re-add.
	if repo.rows["thumbnails/lost.jpg"].Cid != lost {
		t.Errorf("the re-armed row lost its CID; the worker can no longer tell a swap from a repair")
	}
	if kept == lost {
		t.Fatal("fixture bug: the two objects must have different CIDs")
	}
}

// DIRECTION 2 — the node holds a pin no live row accounts for. It is REPORTED
// and never removed: a kubo pin carries no marker separating a Vidra pin from an
// operator's own.
func TestVerifyPinsReportsStraysAndRemovesNothing(t *testing.T) {
	repo, client := newFakeRepo(), ipfs.NewFakeIPFSClient()
	svc := New(repo, &fakeLookups{}, newBlobs(t), client, testConfig())

	seedPinnedOnNode(t, repo, client, "thumbnails/known.jpg", "known-bytes")
	stray, err := client.Add(context.Background(), "operators-own.bin", strings.NewReader("not ours"))
	if err != nil {
		t.Fatalf("fake Add: %v", err)
	}

	results := svc.VerifyPins(context.Background())
	if results[0].Strays != 1 {
		t.Fatalf("strays = %d, want 1", results[0].Strays)
	}
	pins, err := client.ListPins(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListPins: %v", err)
	}
	if _, still := pins[stray.CID]; !still {
		t.Fatal("the sweep unpinned a stray; it must only ever report one")
	}
	if got := svc.GatewayHealth().Strays; got != -1 && got != 1 {
		t.Errorf("stray count did not reach the health record: %d", got)
	}
}

// A CID a 'pending' or 'unpinning' row still claims is NOT a stray: the worker is
// accounting for it right now, and reporting it would fire on every drain.
func TestVerifyPinsDoesNotCallAnInFlightCIDAStray(t *testing.T) {
	repo, client := newFakeRepo(), ipfs.NewFakeIPFSClient()
	svc := New(repo, &fakeLookups{}, newBlobs(t), client, testConfig())

	cid := seedPinnedOnNode(t, repo, client, "thumbnails/inflight.jpg", "bytes")
	repo.rows["thumbnails/inflight.jpg"].State = "unpinning"

	results := svc.VerifyPins(context.Background())
	if results[0].Strays != 0 {
		t.Fatalf("strays = %d, want 0 for a CID an unpinning row still claims (%s)", results[0].Strays, cid)
	}
	if results[0].Checked != 0 {
		t.Errorf("checked = %d, want 0: an unpinning row is not verifiable against the node", results[0].Checked)
	}
}

// THE ONE THING A REPAIR PASS MUST NOT DO. With the node unreachable, ListPins
// errors; if the sweep treated that as an empty pinset it would re-arm the entire
// ledger during an outage.
func TestVerifyPinsWritesNothingWhenTheNodeCannotBeListed(t *testing.T) {
	repo, client := newFakeRepo(), ipfs.NewFakeIPFSClient()
	svc := New(repo, &fakeLookups{}, newBlobs(t), client, testConfig())
	seedPinnedOnNode(t, repo, client, "thumbnails/a.jpg", "a")
	seedPinnedOnNode(t, repo, client, "thumbnails/b.jpg", "b")

	client.Down = true
	results := svc.VerifyPins(context.Background())
	if results[0].Checked != 0 || results[0].Rearmed != 0 || results[0].Strays != -1 {
		t.Fatalf("result on a dead node = %+v, want nothing checked, nothing re-armed, strays unknown", results[0])
	}
	for _, key := range []string{"thumbnails/a.jpg", "thumbnails/b.jpg"} {
		if got := repo.state(key); got != "pinned" {
			t.Errorf("%s is %q; the sweep wrote to the ledger while blind to the node", key, got)
		}
	}
}

// The keyset cursor advances and then wraps, so a ledger larger than one batch is
// eventually covered without ever being scanned whole.
func TestVerifyPinsAdvancesAndWrapsItsCursor(t *testing.T) {
	repo, client := newFakeRepo(), ipfs.NewFakeIPFSClient()
	svc := New(repo, &fakeLookups{}, newBlobs(t), client, testConfig())
	for _, key := range []string{"thumbnails/a.jpg", "thumbnails/b.jpg"} {
		seedPinnedOnNode(t, repo, client, key, key)
	}

	// A batch of one forces the paging the production batch of 500 hides.
	svc.setVerifyCursor(networkPublic, "")
	rows, _ := repo.ListIPFSPinsForVerify(context.Background(), sqlcgen.ListIPFSPinsForVerifyParams{
		Network: networkPublic, BatchSize: 1,
	})
	if len(rows) != 1 || rows[0].ObjectKey != "thumbnails/a.jpg" {
		t.Fatalf("first page = %+v, want the first key in object-key order", rows)
	}
	svc.setVerifyCursor(networkPublic, rows[0].ObjectKey)
	rows, _ = repo.ListIPFSPinsForVerify(context.Background(), sqlcgen.ListIPFSPinsForVerifyParams{
		Network: networkPublic, AfterObjectKey: svc.verifyCursor(networkPublic), BatchSize: 1,
	})
	if len(rows) != 1 || rows[0].ObjectKey != "thumbnails/b.jpg" {
		t.Fatalf("second page = %+v, want the cursor to have advanced", rows)
	}

	// A short page is the tail, and resets the cursor for the next full pass.
	res := svc.VerifyPins(context.Background())
	if !res[0].Wrapped || svc.verifyCursor(networkPublic) != "" {
		t.Errorf("a short page must wrap the cursor; wrapped=%v cursor=%q", res[0].Wrapped, svc.verifyCursor(networkPublic))
	}
}
