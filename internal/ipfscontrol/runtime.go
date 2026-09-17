package ipfscontrol

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type Management struct {
	Mode                  string         `json:"mode"`
	Available             bool           `json:"available"`
	DesiredState          string         `json:"desired_state"`
	ObservedState         string         `json:"observed_state"`
	AppliedConfigRevision int64          `json:"applied_config_revision"`
	Operation             *HostOperation `json:"operation"`
	LastErrorCode         *string        `json:"last_error_code"`
	ObservedAt            *time.Time     `json:"observed_at"`
}
type Capacity struct {
	BudgetBytes           int64   `json:"budget_bytes"`
	RepoUsedBytes         *int64  `json:"repo_used_bytes"`
	ReservedBytes         int64   `json:"reserved_bytes"`
	FilesystemFreeBytes   *int64  `json:"filesystem_free_bytes"`
	MinFreeBytes          int64   `json:"min_free_bytes"`
	AdmissionPausedReason *string `json:"admission_paused_reason"`
}
type Runtime struct {
	ConfigRevision int64      `json:"config_revision"`
	Management     Management `json:"management"`
	Capacity       Capacity   `json:"capacity"`
}

func (s *Service) Runtime(ctx context.Context) (Runtime, error) {
	doc, err := s.Config(ctx)
	if err != nil {
		return Runtime{}, err
	}
	out := Runtime{ConfigRevision: doc.Revision, Management: Management{Mode: doc.Config.Provider, DesiredState: "running", ObservedState: "unknown"}, Capacity: Capacity{BudgetBytes: doc.Config.BudgetBytes, MinFreeBytes: doc.Config.MinFreeBytes}}
	op, err := s.repo.LatestIPFSControlOperation(ctx)
	if err == nil {
		out.Management.Operation = operationView(op)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	// External nodes keep their existing mirror status and admission behavior until
	// explicitly opting into policy; missing a local manager is not their failure.
	if doc.Config.Provider == "external" {
		out.Management.DesiredState = "external"
		return out, nil
	}
	unavailable := "node_unavailable"
	out.Capacity.AdmissionPausedReason = &unavailable
	if s.host == nil {
		out.Management.Mode = "unavailable"
		return out, nil
	}
	host, err := s.host.Status(ctx)
	if err != nil {
		out.Management.LastErrorCode = &unavailable
		return out, nil
	}
	if err = host.Validate(); err != nil {
		out.Management.LastErrorCode = &unavailable
		return out, nil
	}
	out.Management.Available = true
	out.Management.ObservedState = host.ObservedState
	out.Management.AppliedConfigRevision = host.AppliedConfigRevision
	out.Management.LastErrorCode = host.LastErrorCode
	out.Management.ObservedAt = &host.ObservedAt
	out.Capacity.RepoUsedBytes = host.RepoUsedBytes
	out.Capacity.FilesystemFreeBytes = host.FilesystemFreeBytes
	reason := ""
	switch {
	case host.ObservedState != "running":
		reason = "node_unavailable"
	case time.Since(host.ObservedAt) > 30*time.Second || time.Until(host.ObservedAt) > 5*time.Second:
		reason = "stale_capacity"
	case host.AppliedConfigRevision != doc.Revision:
		reason = "configuration_pending"
	case host.RepoUsedBytes == nil || host.FilesystemFreeBytes == nil:
		reason = "capacity_unknown"
	case *host.RepoUsedBytes >= doc.Config.BudgetBytes:
		reason = "budget_exhausted"
	case *host.FilesystemFreeBytes < doc.Config.MinFreeBytes:
		reason = "filesystem_headroom"
	}
	out.Capacity.AdmissionPausedReason = nil
	if reason != "" {
		out.Capacity.AdmissionPausedReason = &reason
	}
	return out, nil
}
