package ipfscontrol

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type controlRepoFake struct {
	Repository
	saved    sqlcgen.IpfsControlConfig
	next     sqlcgen.IpfsControlOperation
	observed sqlcgen.ObserveIPFSControlOperationParams
}

func (r *controlRepoFake) GetIPFSControlConfig(context.Context) (sqlcgen.IpfsControlConfig, error) {
	return r.saved, nil
}
func (r *controlRepoFake) LatestIPFSControlOperation(context.Context) (sqlcgen.IpfsControlOperation, error) {
	return sqlcgen.IpfsControlOperation{}, pgx.ErrNoRows
}
func (r *controlRepoFake) NextIPFSControlOperation(context.Context) (sqlcgen.IpfsControlOperation, error) {
	return r.next, nil
}
func (r *controlRepoFake) ObserveIPFSControlOperation(_ context.Context, p sqlcgen.ObserveIPFSControlOperationParams) (int64, error) {
	r.observed = p
	return 1, nil
}

type controlHostFake struct {
	status HostStatus
	err    error
	calls  int
}

func (h *controlHostFake) Status(context.Context) (HostStatus, error) { return h.status, h.err }
func (h *controlHostFake) Dispatch(context.Context, string, HostEnvelope) (HostOperation, error) {
	h.calls++
	return HostOperation{}, h.err
}
func controlConfig() Config {
	return Config{Provider: "internal", BudgetBytes: 20 << 30, MinFreeBytes: 20 << 30, CopyBytesPerSecond: 2 << 20, Workers: 1}
}

func TestControlUnavailableCapacityIsNotZero(t *testing.T) {
	b, _ := json.Marshal(controlConfig())
	repo := &controlRepoFake{saved: sqlcgen.IpfsControlConfig{Config: b, Revision: 2, PolicyActive: true}}
	host := &controlHostFake{err: errors.New("unavailable")}
	svc := NewService(repo, host, controlConfig())
	got, err := svc.Runtime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Management.Available || got.Capacity.RepoUsedBytes != nil || got.Capacity.FilesystemFreeBytes != nil || got.Capacity.AdmissionPausedReason == nil {
		t.Fatalf("unknown capacity reported healthy: %+v", got)
	}
}
func TestControlNeverDispatchesSupersededConfig(t *testing.T) {
	b, _ := json.Marshal(controlConfig())
	id := uuid.New()
	repo := &controlRepoFake{saved: sqlcgen.IpfsControlConfig{Config: b, Revision: 3, PolicyActive: true}, next: sqlcgen.IpfsControlOperation{ID: id, ConfigRevision: 2, Sequence: 10, Config: b, State: "pending"}}
	host := &controlHostFake{}
	if err := NewService(repo, host, controlConfig()).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if host.calls != 0 || repo.observed.ID != id || repo.observed.State != "failed" || repo.observed.ErrorCode == nil || *repo.observed.ErrorCode != "superseded" {
		t.Fatalf("stale intent dispatched: %+v", repo.observed)
	}
}
func TestControlCompletesOnlyMatchingHostOperation(t *testing.T) {
	b, _ := json.Marshal(controlConfig())
	id := uuid.New()
	ids := id.String()
	repo := &controlRepoFake{saved: sqlcgen.IpfsControlConfig{Config: b, Revision: 2, PolicyActive: true}, next: sqlcgen.IpfsControlOperation{ID: id, ConfigRevision: 2, Sequence: 10, Config: b, State: "running"}}
	host := &controlHostFake{status: HostStatus{ProtocolVersion: 1, ObservedState: "running", ObservedAt: time.Now(), AppliedConfigRevision: 2, LastOperationID: &ids, LastOperationSequence: 10, Operation: &HostOperation{ID: ids, Sequence: 10, ConfigRevision: 2, State: "succeeded"}}}
	if err := NewService(repo, host, controlConfig()).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.observed.State != "succeeded" || host.calls != 0 {
		t.Fatal("matching completion was not consumed")
	}
	host.status.Operation.ConfigRevision = 1
	repo.observed = sqlcgen.ObserveIPFSControlOperationParams{}
	if err := NewService(repo, host, controlConfig()).Tick(context.Background()); err == nil {
		t.Fatal("mismatched revision certified success")
	}
	if repo.observed.State == "succeeded" {
		t.Fatal("mismatched revision committed success")
	}
}

func TestControlSuccessRequiresAppliedRevision(t *testing.T) {
	b, _ := json.Marshal(controlConfig())
	id := uuid.New()
	ids := id.String()
	repo := &controlRepoFake{saved: sqlcgen.IpfsControlConfig{Config: b, Revision: 2, PolicyActive: true}, next: sqlcgen.IpfsControlOperation{ID: id, ConfigRevision: 2, Sequence: 10, Config: b, State: "running"}}
	host := &controlHostFake{status: HostStatus{ProtocolVersion: 1, ObservedState: "running", ObservedAt: time.Now(), AppliedConfigRevision: 1, LastOperationID: &ids, LastOperationSequence: 10, Operation: &HostOperation{ID: ids, Sequence: 10, ConfigRevision: 2, State: "succeeded"}}}
	if err := NewService(repo, host, controlConfig()).Tick(context.Background()); err == nil {
		t.Fatal("unapplied configuration certified success")
	}
	if repo.observed.State == "succeeded" {
		t.Fatal("unapplied configuration committed success")
	}
}
