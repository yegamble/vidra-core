package ipfsmirror

import (
	"context"

	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// reasonBudgetExhausted is the pause reason; it is the managed path's own word for
// the same condition (ipfscontrol.Runtime), so the log line and the
// vidra_ipfs_admission_paused gauge read identically in both modes.
const reasonBudgetExhausted = "budget_exhausted"

// unpinClaimer is the repository's removals-only claim. Optional so a repository
// double without it keeps the old (claim everything) behaviour.
type unpinClaimer interface {
	ClaimDueIPFSUnpins(context.Context, sqlcgen.ClaimDueIPFSUnpinsParams) ([]sqlcgen.ClaimDueIPFSUnpinsRow, error)
}

// unmanagedOverBudget is the ceiling for a node nobody manages (Tier A F3). The
// legacy drain had NO admission at all: it added until the volume was full, and the
// volume is usually shared with Postgres and the media scratch.
//
// It asks the node for its repo/stat RepoSize once per drain tick and reports
// whether it has reached IPFS_REPO_BUDGET_BYTES. Over it, the drain claims
// REMOVALS ONLY (ClaimDueIPFSUnpins): UNPINS ARE NEVER GATED or delayed - a
// takedown must not wait on disk space or queue behind adds. Pending rows are not
// touched, so they resume by themselves when space frees (an unpin + gc, or a
// raised budget).
//
// What it is NOT. It is a repo-size budget, not a free-space floor: core reaches
// the node over RPC and cannot statvfs its filesystem, and RepoSize excludes
// flatfs slack and inodes - so the operator sets the budget BELOW disk size minus
// database growth minus media scratch (docs/operations.md). It is also a soft
// ceiling: it is probed per tick, so a tick's batch of adds (up to
// IPFS_PIN_CONCURRENCY trees at a time) can overshoot it.
//
// FAIL OPEN. If the node will not report a size (down, or a client without the
// RepoSizer capability) adds proceed: a stat failure predicts nothing about the
// disk, and an add against a dead node fails and backs off on its own. Only the
// PUBLIC node is budgeted (the private tier has its own node and no budget knob).
func (s *Service) unmanagedOverBudget(ctx context.Context, nc netClient) bool {
	if s.repoBudget <= 0 || nc.network != networkPublic {
		return false
	}
	sizer, ok := nc.client.(ipfs.RepoSizer)
	if !ok {
		return false
	}
	used, err := sizer.RepoSize(ctx)
	if err != nil {
		return false
	}
	if used >= s.repoBudget {
		s.noteAdmissionReason(reasonBudgetExhausted, s.repoBudget, &used, 0, nil)
		return true
	}
	s.noteAdmissionReason("", s.repoBudget, &used, 0, nil)
	return false
}

// UnmanagedCapacity reports the public node's live repo size against the F3
// ceiling, for the capacity gauges. ok=false when no ceiling is configured or the
// node cannot report a size.
func (s *Service) UnmanagedCapacity(ctx context.Context) (used, budget int64, ok bool) {
	if s.repoBudget <= 0 || s.client == nil {
		return 0, 0, false
	}
	sizer, isSizer := s.client.(ipfs.RepoSizer)
	if !isSizer {
		return 0, 0, false
	}
	used, err := sizer.RepoSize(ctx)
	if err != nil {
		return 0, 0, false
	}
	return used, s.repoBudget, true
}
