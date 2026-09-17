package ipfsmirror

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/ipfscontrol"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type copyCleanupRepo interface {
	GetIPFSCapacity(context.Context) (sqlcgen.IpfsCapacity, error)
	BeginIPFSCopyCleanup(context.Context, sqlcgen.BeginIPFSCopyCleanupParams) (sqlcgen.IpfsCapacity, error)
	NextIPFSCopyCleanup(context.Context) (sqlcgen.IpfsCopyCleanup, error)
	IPFSReturnedRootReferenced(context.Context, string) (bool, error)
	FinishIPFSCopyCleanup(context.Context, sqlcgen.FinishIPFSCopyCleanupParams) (int64, error)
	RecoverIPFSCopyCleanup(context.Context, pgtype.UUID) (int64, error)
	GetIPFSControlOperation(context.Context, uuid.UUID) (sqlcgen.IpfsControlOperation, error)
}
type copyCleanupControl interface {
	Request(context.Context, string, int64, uuid.UUID, uuid.UUID) (ipfscontrol.HostOperation, error)
}

// reconcileCopyCleanup runs even while publication is paused. The capacity row
// fences new copies during unpin/GC; a timed-out RPC keeps that fence until a
// later confirmed node restart proves the old request cannot still be running.
func reconcileCopyCleanup(ctx context.Context, r copyCleanupRepo, control copyCleanupControl, client ipfs.Client, doc ipfscontrol.Document, host ipfscontrol.HostStatus) (bool, error) {
	capacity, err := r.GetIPFSCapacity(ctx)
	if err != nil {
		return true, err
	}
	if capacity.CleanupPending == 0 && !capacity.MaintenanceToken.Valid {
		return false, nil
	}
	if !doc.PolicyActive || doc.Config.Provider != "internal" || host.ObservedState != "running" || host.AppliedConfigRevision != doc.Revision || time.Since(host.ObservedAt) > 30*time.Second || time.Until(host.ObservedAt) > 5*time.Second || capacity.ActiveClaims > 0 {
		return true, nil
	}
	if capacity.MaintenanceToken.Valid {
		if !capacity.MaintenanceUntil.Valid || time.Now().Before(capacity.MaintenanceUntil.Time) {
			return true, nil
		}
		if op := host.Operation; op != nil && op.State == "succeeded" && host.LastOperationSequence > capacity.MaintenanceHostSequence {
			id, e := uuid.Parse(op.ID)
			if e != nil {
				return true, e
			}
			saved, e := r.GetIPFSControlOperation(ctx, id)
			if e != nil {
				return true, e
			}
			if saved.Action == "restart" && saved.Sequence == host.LastOperationSequence && saved.ConfigRevision == doc.Revision {
				_, e = r.RecoverIPFSCopyCleanup(ctx, capacity.MaintenanceToken)
				return true, e
			}
		}
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("vidra-ipfs-cleanup:%d:%s:%d", doc.Revision, uuid.UUID(capacity.MaintenanceToken.Bytes), capacity.MaintenanceHostSequence)))
		_, err = control.Request(ctx, "restart", doc.Revision, id, uuid.Nil)
		return true, err
	}
	capacity, err = r.BeginIPFSCopyCleanup(ctx, sqlcgen.BeginIPFSCopyCleanupParams{MaintenanceToken: pgUUID(uuid.New()), HostSequence: host.LastOperationSequence, ConfigRevision: doc.Revision})
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	row, err := r.NextIPFSCopyCleanup(ctx)
	if err != nil {
		return true, err
	}
	referenced, err := r.IPFSReturnedRootReferenced(ctx, row.Cid)
	if err != nil {
		return true, err
	}
	if !referenced {
		// A returned managed root is the only ownership evidence used here. Never
		// sweep arbitrary node pins, including manual pins absent from our ledger.
		rpcctx, cancel := context.WithTimeout(ctx, 11*time.Minute)
		err = client.Unpin(rpcctx, row.Cid)
		if err == nil {
			_, err = client.RepoGC(rpcctx)
		}
		cancel()
		if err != nil {
			return true, err
		}
	}
	n, err := r.FinishIPFSCopyCleanup(ctx, sqlcgen.FinishIPFSCopyCleanupParams{MaintenanceToken: capacity.MaintenanceToken, ClaimToken: row.ClaimToken, Cid: row.Cid})
	if err == nil && n != 1 {
		err = errors.New("ipfs cleanup ownership changed")
	}
	return true, err // require a fresh post-GC observation before any admission
}
