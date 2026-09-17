//go:build integration

package store

import (
	"context"
	"strings"
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

type managedTestHost struct{ q *sqlcgen.Queries }

func (h managedTestHost) Status(ctx context.Context) (ipfscontrol.HostStatus, error) {
	c, err := h.q.GetIPFSControlConfig(ctx)
	if err != nil {
		return ipfscontrol.HostStatus{}, err
	}
	op, err := h.q.LatestIPFSControlOperation(ctx)
	if err != nil {
		return ipfscontrol.HostStatus{}, err
	}
	used, free := int64(0), int64(100<<20)
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
	_, err = st.Pool.Exec(ctx, "TRUNCATE ipfs_control_operations,ipfs_control_config,ipfs_capacity")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.Pool.Exec(context.Background(), "TRUNCATE ipfs_control_operations,ipfs_control_config,ipfs_capacity")
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
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for object, data := range map[string]string{master: "#EXTM3U\n720/seg.ts", strings.TrimSuffix(master, "imported-master.m3u8") + "720/seg.ts": "media"} {
		if _, err = blobs.Put(ctx, object, strings.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	c := ipfscontrol.Config{Provider: "internal", Enabled: true, AutoPinNew: true, DemandPin: true, BudgetBytes: 8 << 20, MinFreeBytes: 2 << 20, CopyBytesPerSecond: 1 << 20, Workers: 1}
	control := ipfscontrol.NewService(q, managedTestHost{q}, c)
	doc, err := control.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	doc, err = control.Save(ctx, doc.Revision, c, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	node := ipfs.NewFakeIPFSClient()
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
