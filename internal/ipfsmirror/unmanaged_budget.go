package ipfsmirror

import (
	"context"

	"github.com/vidra/vidra-core/internal/ipfs"
)

// reasonBudgetExhausted is the pause reason; it is the managed path's own word for
// the same condition (ipfscontrol.Runtime), so the log line and the
// vidra_ipfs_admission_paused gauge read identically in both modes.
const reasonBudgetExhausted = "budget_exhausted"

// unmanagedAdmits is the ceiling for a node nobody manages (Tier A F3). The legacy
// drain had NO admission at all: it added until the volume was full, and the
// volume is usually shared with Postgres and the media scratch.
//
// It asks the node for its repo/stat RepoSize before each ADD and refuses once it
// reaches IPFS_REPO_BUDGET_BYTES. UNPINS ARE NEVER GATED — a takedown must not wait
// on disk space. A refused row is not touched: the claim already leased it
// (leaseSeconds), so it is retried after the lease, and it resumes by itself when
// space frees (an unpin + gc, or a raised budget).
//
// What it is NOT. It is a repo-size budget, not a free-space floor: core reaches
// the node over RPC and cannot statvfs its filesystem, and RepoSize excludes
// flatfs slack and inodes — so the operator sets the budget BELOW disk size minus
// database growth minus media scratch (docs/operations.md). It is also a soft
// ceiling: it is checked per add, so concurrent adds in flight can overshoot by up
// to IPFS_PIN_CONCURRENCY trees.
//
// FAIL OPEN. If the node will not report a size (down, or a client without the
// RepoSizer capability) the add proceeds: a stat failure predicts nothing about
// the disk, and an add against a dead node fails and backs off on its own. Only
// the PUBLIC node is budgeted (the private tier has its own node and no budget
// knob yet).
func (s *Service) unmanagedAdmits(ctx context.Context, nc netClient) bool {
	if s.repoBudget <= 0 || nc.network != networkPublic {
		return true
	}
	sizer, ok := nc.client.(ipfs.RepoSizer)
	if !ok {
		return true
	}
	used, err := sizer.RepoSize(ctx)
	if err != nil {
		return true
	}
	if used >= s.repoBudget {
		s.noteAdmissionReason(reasonBudgetExhausted, s.repoBudget, &used, 0, nil)
		return false
	}
	s.noteAdmissionReason("", s.repoBudget, &used, 0, nil)
	return true
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
