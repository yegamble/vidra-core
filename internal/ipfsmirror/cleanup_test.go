package ipfsmirror

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/ipfscontrol"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type cleanupFake struct {
	copyCleanupRepo
	capacity            sqlcgen.IpfsCapacity
	referenced          bool
	finished, recovered bool
	action              string
}

func (f *cleanupFake) GetIPFSCapacity(context.Context) (sqlcgen.IpfsCapacity, error) {
	return f.capacity, nil
}
func (f *cleanupFake) BeginIPFSCopyCleanup(_ context.Context, p sqlcgen.BeginIPFSCopyCleanupParams) (sqlcgen.IpfsCapacity, error) {
	f.capacity.MaintenanceToken = p.MaintenanceToken
	return f.capacity, nil
}
func (f *cleanupFake) NextIPFSCopyCleanup(context.Context) (sqlcgen.IpfsCopyCleanup, error) {
	return sqlcgen.IpfsCopyCleanup{ClaimToken: uuid.New(), Cid: "test-root"}, nil
}
func (f *cleanupFake) IPFSReturnedRootReferenced(context.Context, string) (bool, error) {
	return f.referenced, nil
}
func (f *cleanupFake) FinishIPFSCopyCleanup(context.Context, sqlcgen.FinishIPFSCopyCleanupParams) (int64, error) {
	f.finished = true
	return 1, nil
}
func (f *cleanupFake) RecoverIPFSCopyCleanup(context.Context, pgtype.UUID) (int64, error) {
	f.recovered = true
	return 1, nil
}
func (f *cleanupFake) GetIPFSControlOperation(_ context.Context, id uuid.UUID) (sqlcgen.IpfsControlOperation, error) {
	return sqlcgen.IpfsControlOperation{ID: id, Action: f.action, Sequence: 3, ConfigRevision: 2}, nil
}

type cleanupClient struct {
	ipfs.Client
	unpins, gcs int
	fail        bool
	absent      bool
}

func (c *cleanupClient) Unpin(context.Context, string) error {
	c.unpins++
	if c.fail {
		return errors.New("unknown RPC outcome")
	}
	return nil
}
func (c *cleanupClient) RepoGC(context.Context) (int64, error) { c.gcs++; return 1, nil }
func (c *cleanupClient) ListPins(context.Context, int) (map[string]struct{}, error) {
	if c.absent {
		return map[string]struct{}{}, nil
	}
	return nil, errors.New("node unavailable")
}

type cleanupControl struct {
	requested int
	id        uuid.UUID
}

func (c *cleanupControl) Request(_ context.Context, action string, _ int64, id, _ uuid.UUID) (ipfscontrol.HostOperation, error) {
	c.requested++
	c.id = id
	return ipfscontrol.HostOperation{}, nil
}

func TestReturnedCopyCleanupProtectsLedgerAndRetainsFailedRPC(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		referenced, fail, absent bool
		unpins, gcs              int
		finish                   bool
	}{{"shared-or-newer-ledger-root", true, false, false, 0, 0, true}, {"orphan", false, false, false, 1, 1, true}, {"unknown-outcome", false, true, false, 1, 0, false}, {"already-unpinned", false, true, true, 1, 1, true}} {
		t.Run(tc.name, func(t *testing.T) {
			r := &cleanupFake{capacity: sqlcgen.IpfsCapacity{CleanupPending: 1}, referenced: tc.referenced}
			c := &cleanupClient{fail: tc.fail, absent: tc.absent}
			doc := ipfscontrol.Document{Revision: 2, PolicyActive: true, Config: ipfscontrol.Config{Provider: "internal"}}
			host := ipfscontrol.HostStatus{ObservedState: "running", AppliedConfigRevision: 2, ObservedAt: time.Now()}
			pending, err := reconcileCopyCleanup(context.Background(), r, &cleanupControl{}, c, doc, host)
			if !pending || (err != nil) != (tc.fail && !tc.absent) || c.unpins != tc.unpins || c.gcs != tc.gcs || r.finished != tc.finish || !r.capacity.MaintenanceToken.Valid {
				t.Fatalf("pending=%v err=%v calls=%+v repo=%+v", pending, err, c, r)
			}
		})
	}
}

func TestReturnedCopyCleanupNeedsConfirmedRestartNotExpiryOrApply(t *testing.T) {
	for _, action := range []string{"apply", "restart"} {
		t.Run(action, func(t *testing.T) {
			r := &cleanupFake{action: action, capacity: sqlcgen.IpfsCapacity{CleanupPending: 1, MaintenanceToken: pgUUID(uuid.New()), MaintenanceUntil: pgtype.Timestamptz{Time: time.Now().Add(-time.Second), Valid: true}, MaintenanceHostSequence: 1}}
			control := &cleanupControl{}
			c := &cleanupClient{}
			doc := ipfscontrol.Document{Revision: 2, PolicyActive: true, Config: ipfscontrol.Config{Provider: "internal"}}
			id := uuid.NewString()
			host := ipfscontrol.HostStatus{ObservedState: "running", AppliedConfigRevision: 2, ObservedAt: time.Now(), LastOperationSequence: 3, LastOperationID: &id, Operation: &ipfscontrol.HostOperation{ID: id, Sequence: 3, ConfigRevision: 2, State: "succeeded"}}
			for range 2 {
				pending, err := reconcileCopyCleanup(context.Background(), r, control, c, doc, host)
				if !pending || err != nil {
					t.Fatalf("%v %v", pending, err)
				}
			}
			if r.recovered != (action == "restart") || control.requested != map[string]int{"apply": 2, "restart": 0}[action] || c.unpins != 0 {
				t.Fatalf("unsafe recovery repo=%+v control=%+v", r, control)
			}
			if action == "apply" && control.id == uuid.Nil {
				t.Fatal("missing replay-safe restart ID")
			}
		})
	}
}
