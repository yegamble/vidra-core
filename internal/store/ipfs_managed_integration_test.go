//go:build integration

package store

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/ipfscontrol"
	"github.com/vidra/vidra-core/internal/ipfsmirror"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type managedTestHost struct {
	q    *sqlcgen.Queries
	used int64
}

type managedTestNode struct {
	ipfs.Client
	after func(string)
}

func (n managedTestNode) Add(ctx context.Context, name string, r io.Reader) (ipfs.AddResult, error) {
	result, err := n.Client.Add(ctx, name, r)
	if err == nil {
		n.after(result.CID)
	}
	return result, err
}
func (n managedTestNode) AddDirectory(ctx context.Context, entries []ipfs.DirEntry) (ipfs.AddResult, error) {
	result, err := n.Client.AddDirectory(ctx, entries)
	if err == nil {
		n.after(result.CID)
	}
	return result, err
}

func (h managedTestHost) Status(ctx context.Context) (ipfscontrol.HostStatus, error) {
	c, err := h.q.GetIPFSControlConfig(ctx)
	if err != nil {
		return ipfscontrol.HostStatus{}, err
	}
	op, err := h.q.LatestIPFSControlOperation(ctx)
	if err != nil {
		return ipfscontrol.HostStatus{}, err
	}
	used, free := h.used, int64(100<<20)
	id := op.ID.String()
	return ipfscontrol.HostStatus{ProtocolVersion: 1, ObservedState: "running", AppliedConfigRevision: c.Revision, LastOperationID: &id, LastOperationSequence: op.Sequence, Operation: &ipfscontrol.HostOperation{ID: id, Sequence: op.Sequence, ConfigRevision: op.ConfigRevision, State: "succeeded"}, RepoUsedBytes: &used, FilesystemFreeBytes: &free, ObservedAt: time.Now()}, nil
}
func (managedTestHost) Dispatch(context.Context, string, ipfscontrol.HostEnvelope) (ipfscontrol.HostOperation, error) {
	panic("dispatch not used")
}

func TestIPFSManagedCopyDemandWithdrawalAndCrashRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	q := st.Queries()
	_, err = st.Pool.Exec(ctx, "TRUNCATE ipfs_copy_cleanup, ipfs_control_operations,ipfs_control_config,ipfs_capacity")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.Pool.Exec(context.Background(), "TRUNCATE ipfs_copy_cleanup, ipfs_control_operations,ipfs_control_config,ipfs_capacity")
	}()
	id, cleanup := seedVideoRow(t, st)
	defer cleanup()
	key := media.HLSKeyPrefix(id) + "/"
	master := "streaming-playlists/hls/" + id.String() + "/imported-master.m3u8"
	defer func() { _, _ = st.Pool.Exec(context.Background(), "DELETE FROM media_ipfs_pins WHERE video_id=$1", id) }()
	_, err = st.Pool.Exec(ctx, "UPDATE videos SET privacy='public',state='published' WHERE id=$1", id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Pool.Exec(ctx, "INSERT INTO streaming_playlists(video_id,master_key,state) VALUES($1,$2,'ready')", id, master)
	if err != nil {
		t.Fatal(err)
	}
	local, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var blobs storage.Backend = local
	if endpoint := os.Getenv("S3_TEST_ENDPOINT"); endpoint != "" {
		s3, e := storage.NewS3(storage.S3Config{Endpoint: endpoint, Bucket: "vidra-ipfs-managed-test", AccessKey: "vidra", SecretKey: "vidra-dev-secret", ForcePathStyle: true})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s3.EnsureBucket(ctx); e != nil {
			t.Fatal(e)
		}
		blobs = s3
		defer s3.DeletePrefix(context.Background(), "streaming-playlists/hls/"+id.String())
	}
	for object, data := range map[string]string{master: "#EXTM3U\n720/seg.ts", strings.TrimSuffix(master, "imported-master.m3u8") + "720/seg.ts": "media"} {
		if _, err = blobs.Put(ctx, object, strings.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	c := ipfscontrol.Config{Provider: "internal", Enabled: true, AutoPinNew: true, DemandPin: true, BudgetBytes: 8 << 20, MinFreeBytes: 2 << 20, CopyBytesPerSecond: 1 << 20, Workers: 1}
	control := ipfscontrol.NewService(q, managedTestHost{q: q}, c)
	doc, err := control.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	doc, err = control.Save(ctx, doc.Revision, c, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	var nodeClient ipfs.Client = ipfs.NewFakeIPFSClient()
	if endpoint := os.Getenv("IPFS_TEST_API_URL"); endpoint != "" {
		nodeClient = ipfs.NewKuboClient(endpoint, &http.Client{Timeout: 5 * time.Second})
	}
	var changeDuringCopy atomic.Int32
	var returnedRoot atomic.Value
	hookResult := make(chan error, 1)
	node := managedTestNode{Client: nodeClient, after: func(cid string) {
		returnedRoot.Store(cid)
		switch changeDuringCopy.Load() {
		case 1:
			_, e := st.Pool.Exec(ctx, "UPDATE videos SET privacy='private' WHERE id=$1", id)
			hookResult <- e
		case 2:
			_, e := st.Pool.Exec(ctx, "UPDATE streaming_playlists SET master_key=$2 WHERE video_id=$1", id, master+"-new")
			hookResult <- e
		case 3:
			_, e := st.Pool.Exec(ctx, "UPDATE media_ipfs_pins SET lease_until=now()-interval '1 second' WHERE object_key=$1", key)
			hookResult <- e
		}
	}}
	mirror := ipfsmirror.New(q, ipfsmirror.NewSQLLookups(q), blobs, node, ipfsmirror.Config{GatewayURL: "https://gateway.test", AddTimeout: time.Second})
	mirror.ConfigureControl(control)
	if err = mirror.DemandPublicVideo(ctx, id); err != nil {
		t.Fatal(err)
	}
	row, err := q.GetIPFSPinByObjectKey(ctx, key)
	if err != nil || row.PolicyReason != "demand" {
		t.Fatalf("demand intent %+v %v", row, err)
	}
	if _, err = mirror.DrainDue(ctx, 8); err != nil {
		t.Fatal(err)
	}
	for {
		row, err = q.GetIPFSPinByObjectKey(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if row.State == "pinned" && !row.ClaimToken.Valid {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("copy did not finish: %+v", row)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if row.CommittedGeneration != master {
		t.Fatalf("generation %+v", row)
	}
	url, ok, err := mirror.PublicPlaybackHLS(ctx, id, master)
	if err != nil || !ok || !strings.HasSuffix(url, "/imported-master.m3u8") {
		t.Fatalf("playback %q %v %v", url, ok, err)
	}
	if _, err = mirror.DrainDue(ctx, 8); err != nil {
		t.Fatal(err)
	}
	if pinned, e := node.IsPinned(ctx, row.Cid); e != nil || !pinned {
		t.Fatalf("cleanup removed committed root: %v %v", pinned, e)
	}
	// Publication pause retains valid pins, but a withdrawal still drains and GC
	// releases bytes. The gateway gate denies immediately, before the drain.
	c.Enabled = false
	doc, err = control.Save(ctx, doc.Revision, c, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err = mirror.PublicPlaybackHLS(ctx, id, master); err != nil || !ok {
		t.Fatalf("pause removed retained delivery: %v", err)
	}
	_, err = st.Pool.Exec(ctx, "UPDATE videos SET privacy='private' WHERE id=$1", id)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err = mirror.PublicGatewayRootAllowed(ctx, row.Cid); err != nil || ok {
		t.Fatalf("withdrawn root allowed %v", err)
	}
	if err = q.EnqueueIPFSUnpin(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err = mirror.DrainDue(ctx, 8); err != nil {
		t.Fatal(err)
	}
	row, err = q.GetIPFSPinByObjectKey(ctx, key)
	if err != nil || row.State != "unpinned" {
		t.Fatalf("paused withdrawal %+v %v", row, err)
	}
	for _, change := range []int32{1, 2, 3} {
		// A missed enqueue cannot let a completion overwrite newly private facts.
		// This interleaves the visibility commit AFTER the node returned its CID.
		_, err = st.Pool.Exec(ctx, "UPDATE videos SET privacy='public' WHERE id=$1", id)
		if err != nil {
			t.Fatal(err)
		}
		_, err = st.Pool.Exec(ctx, "UPDATE streaming_playlists SET master_key=$2 WHERE video_id=$1", id, master)
		if err != nil {
			t.Fatal(err)
		}
		c.Enabled = true
		doc, err = control.Save(ctx, doc.Revision, c, uuid.Nil)
		if err != nil {
			t.Fatal(err)
		}
		changeDuringCopy.Store(change)
		if change == 3 {
			if _, err = blobs.Put(ctx, strings.TrimSuffix(master, "imported-master.m3u8")+"720/seg.ts", strings.NewReader("different payload")); err != nil {
				t.Fatal(err)
			}
		}
		if err = mirror.OnTranscodeComplete(ctx, id); err != nil {
			t.Fatal(err)
		}
		if _, err = mirror.DrainDue(ctx, 8); err != nil {
			t.Fatal(err)
		}
		select {
		case err = <-hookResult:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("copy callback not reached")
		}
		for {
			row, err = q.GetIPFSPinByObjectKey(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			if (((change == 1 && row.State == "unpinned") || (change == 2 && row.State == "pending")) && !row.ClaimToken.Valid) || (change == 3 && row.ClaimToken.Valid && row.CapacityReason == "copy_failed") {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatalf("losing privacy completion did not clean up: %+v", row)
			case <-time.After(10 * time.Millisecond):
			}
		}
		// Lost-lease completion retains its returned CID durably until a proven
		// restart releases capacity, then exclusive cleanup can safely retire it.
		for range 4 {
			if _, err = mirror.DrainDue(ctx, 8); err != nil {
				t.Fatal(err)
			}
		}
		capacity, e := q.GetIPFSCapacity(ctx)
		if e != nil || capacity.ActiveClaims != 0 || capacity.CleanupPending != 0 || capacity.MaintenanceToken.Valid {
			t.Fatalf("copy cleanup did not settle: %+v %v", capacity, e)
		}
		if pinned, e := node.IsPinned(ctx, returnedRoot.Load().(string)); e != nil || pinned {
			t.Fatalf("losing copy pin retained: %v %v", pinned, e)
		}
	}

	c.Enabled = false
	doc, err = control.Save(ctx, doc.Revision, c, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	// A crashed copy retains its reservation until a later confirmed restart.
	_, err = st.Pool.Exec(ctx, "UPDATE media_ipfs_pins SET claim_token=$2, lease_until=now()-interval '1 second', reservation_bytes=100,admitted_host_sequence=$3 WHERE object_key=$1", key, uuid.New(), int64(0))
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Pool.Exec(ctx, "UPDATE ipfs_capacity SET reserved_bytes=100,active_claims=1 WHERE singleton")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = mirror.DrainDue(ctx, 8); err != nil {
		t.Fatal(err)
	}
	row, err = q.GetIPFSPinByObjectKey(ctx, key)
	if err != nil || !row.ClaimToken.Valid {
		t.Fatalf("apply falsely proved restart %+v %v", row, err)
	}
	op, err := q.LatestIPFSControlOperation(ctx)
	if err != nil || op.Action != "restart" {
		t.Fatalf("no recovery restart %+v %v", op, err)
	}
	if _, err = mirror.DrainDue(ctx, 8); err != nil {
		t.Fatal(err)
	}
	row, err = q.GetIPFSPinByObjectKey(ctx, key)
	if err != nil || row.ClaimToken.Valid {
		t.Fatalf("confirmed restart did not release %+v %v", row, err)
	}
}

func TestIPFSManagedLoweredBudgetRetiresColdPinWithoutNewJobs(t *testing.T) {
	ctx := context.Background()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	reset := func() {
		_, e := st.Pool.Exec(ctx, "TRUNCATE ipfs_copy_cleanup,ipfs_control_operations,ipfs_control_config,ipfs_capacity")
		if e != nil {
			t.Fatal(e)
		}
	}
	reset()
	defer reset()
	q := st.Queries()
	key := "budget-test/" + uuid.NewString()
	defer func() { _, _ = st.Pool.Exec(ctx, "DELETE FROM media_ipfs_pins WHERE object_key=$1", key) }()
	_, err = st.Pool.Exec(ctx, "INSERT INTO media_ipfs_pins(object_key,media_class,cid,state,policy_reason,created_at) VALUES($1,'thumbnail',$1,'pinned','new',now()-interval '2 hours')", key)
	if err != nil {
		t.Fatal(err)
	}
	c := ipfscontrol.Config{Provider: "internal", Enabled: true, BudgetBytes: 1 << 20, CopyBytesPerSecond: 1 << 20, Workers: 1}
	control := ipfscontrol.NewService(q, managedTestHost{q: q, used: 2 << 20}, c)
	doc, err := control.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = control.Save(ctx, doc.Revision, c, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	mirror := ipfsmirror.New(q, ipfsmirror.NewSQLLookups(q), nil, ipfs.NewFakeIPFSClient(), ipfsmirror.Config{GatewayURL: "https://gateway.test"})
	mirror.ConfigureControl(control)
	if _, err = mirror.DrainDue(ctx, 8); err != nil {
		t.Fatal(err)
	}
	row, err := q.GetIPFSPinByObjectKey(ctx, key)
	if err != nil || row.State != "unpinning" || row.CapacityReason != "evicted_capacity" {
		t.Fatalf("lowered budget did not retire cold pin: %+v %v", row, err)
	}
}
