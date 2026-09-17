package ipfscontrol

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Tick performs at most one bounded host exchange. Run on the existing elected
// worker; duplicate dispatch after a crash is safe through durable host IDs.
func (s *Service) Tick(ctx context.Context) error {
	if s.host == nil {
		return nil
	}
	op, err := s.repo.NextIPFSControlOperation(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if time.Now().Before(op.NextAttemptAt) {
		return nil
	}
	doc, err := s.Config(ctx)
	if err != nil {
		return err
	}
	observe := func(state, code string) error {
		p := sqlcgen.ObserveIPFSControlOperationParams{ID: op.ID, Sequence: op.Sequence, State: state}
		if code != "" {
			p.ErrorCode = &code
		}
		_, err := s.repo.ObserveIPFSControlOperation(ctx, p)
		return err
	}
	if op.ConfigRevision != doc.Revision || doc.Config.Provider != "internal" {
		return observe("failed", "superseded")
	}
	var saved Config
	if err := json.Unmarshal(op.Config, &saved); err != nil {
		return err
	}
	if saved != doc.Config {
		return errors.New("IPFS operation config does not match revision")
	}
	host, err := s.host.Status(ctx)
	if err != nil {
		return observe("pending", "node_unavailable")
	}
	if err = host.Validate(); err != nil {
		return err
	}
	if host.LastOperationSequence > op.Sequence {
		return observe("failed", "superseded")
	}
	if host.LastOperationSequence == op.Sequence {
		observed := host.Operation
		if observed == nil || observed.ID != op.ID.String() || observed.ConfigRevision != op.ConfigRevision {
			return errors.New("IPFS manager operation identity mismatch")
		}
		if observed.State == "succeeded" && host.AppliedConfigRevision != op.ConfigRevision {
			return errors.New("IPFS manager has not applied the completed revision")
		}
		if observed.State == "failed" {
			return observe("failed", "host_operation_failed")
		}
		return observe(observed.State, "")
	}
	accepted, err := s.host.Dispatch(ctx, op.Action, HostEnvelope{ProtocolVersion: 1, OperationID: op.ID.String(), Sequence: op.Sequence, ConfigRevision: op.ConfigRevision, Config: saved.Host()})
	if err != nil {
		return observe("pending", "manager_unavailable")
	}
	if accepted.ID != op.ID.String() || accepted.Sequence != op.Sequence || accepted.ConfigRevision != op.ConfigRevision {
		return errors.New("IPFS manager accepted a different operation")
	}
	// Dispatch acknowledgement is not proof that the revision is applied.
	// The next status observation binds terminal success to actual host state.
	return observe("running", "")
}
