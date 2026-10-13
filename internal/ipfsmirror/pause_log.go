package ipfsmirror

import "context"

// capacityPauseReasons are the admission-pause reasons that mean "the disk said
// stop", as opposed to an operator's own switch (publication_paused,
// policy_not_adopted) or a transient (configuration_pending, stale_capacity).
// They warn; the rest log at info. The names are ipfscontrol.Runtime's.
var capacityPauseReasons = map[string]bool{
	"budget_exhausted":        true,
	"filesystem_headroom":     true,
	"recovering_copy_cleanup": true,
}

// observeAdmission logs ONE line per pause transition (F4): into a pause, between
// pause reasons, and out of one. Before this a full budget left no trace anywhere
// but the Admin -> IPFS page — pinning simply stopped. The reason comes from
// ipfscontrol.Runtime, the same function the admin page and the
// vidra_ipfs_admission_paused gauge read, so the three cannot disagree. It runs on
// every managed drain pass; a read failure is silent (the next pass retries).
func (s *Service) observeAdmission(ctx context.Context) {
	if s.control == nil {
		return
	}
	rt, err := s.control.Runtime(ctx)
	if err != nil {
		return
	}
	reason := ""
	if rt.Capacity.AdmissionPausedReason != nil {
		reason = *rt.Capacity.AdmissionPausedReason
	}
	s.noteAdmissionReason(reason, rt.Capacity.BudgetBytes, rt.Capacity.RepoUsedBytes, rt.Capacity.ReservedBytes, rt.Capacity.FilesystemFreeBytes)
}

func (s *Service) noteAdmissionReason(reason string, budget int64, used *int64, reserved int64, free *int64) {
	s.pauseMu.Lock()
	prev := s.pauseReason
	s.pauseReason = reason
	s.pauseMu.Unlock()
	if prev == reason {
		return
	}
	attrs := []any{"reason", reason, "previous_reason", prev, "budget_bytes", budget, "reserved_bytes", reserved}
	if used != nil {
		attrs = append(attrs, "repo_used_bytes", *used)
	}
	if free != nil {
		attrs = append(attrs, "filesystem_free_bytes", *free)
	}
	switch {
	case reason == "":
		s.logger.Info("ipfs_admission_resumed", attrs...)
	case capacityPauseReasons[reason]:
		s.logger.Warn("ipfs_admission_paused", append(attrs, "effect", "no new pins are admitted until space frees; existing pins keep serving")...)
	default:
		s.logger.Info("ipfs_admission_paused", attrs...)
	}
}
