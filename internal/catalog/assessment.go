package catalog

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var (
	ErrEpochConflict          = errors.New("catalog durable epoch conflict requires explicit operator recovery")
	ErrReceiverEpochMismatch  = errors.New("catalog receiver epoch requires explicit bootstrap or recovery")
	ErrProducerBehindReceiver = errors.New("catalog receiver observed a generation beyond the source allocator")
	ErrPendingExpired         = errors.New("catalog pending generation expired; capture a higher generation")
	ErrReceiverBehindVerified = errors.New("catalog receiver is behind verified source evidence; require a fresh full")
	ErrReceiverProofMismatch  = errors.New("catalog receiver completion does not match retained source proof")
)

type FullProof struct {
	Identity Identity
	Manifest Manifest
}

type SourceEvidence struct {
	Epoch               uuid.UUID
	AllocatedGeneration int64
	Verified            *FullProof
	Pending             *FullProof
}

// Assessment never grants source read readiness. CanDeliver permits only the
// normal path; recovery delivery requires the controller's durable force fence.
type Assessment struct {
	VerifiedMatches  bool
	PendingCompleted bool
	CanDeliver       bool
}

// AssessReceiver compares durable source proof with one receiver observation.
// It performs no bootstrap, mutation or I/O. Every refusal returns zero flags.
func AssessReceiver(s SourceEvidence, r Status) (Assessment, error) {
	if s.Epoch == uuid.Nil || s.AllocatedGeneration < 0 {
		return Assessment{}, fmt.Errorf("catalog source requires an epoch and nonnegative allocator")
	}
	for _, proof := range []*FullProof{s.Verified, s.Pending} {
		if proof == nil {
			continue
		}
		if err := proof.Identity.Validate(); err != nil {
			return Assessment{}, err
		}
		if proof.Identity.Epoch != s.Epoch || int64(proof.Identity.Generation) > s.AllocatedGeneration {
			return Assessment{}, fmt.Errorf("catalog source proof is outside its current epoch or allocator")
		}
		if err := proof.Manifest.Validate(); err != nil {
			return Assessment{}, err
		}
	}
	if s.Pending != nil && (int64(s.Pending.Identity.Generation) != s.AllocatedGeneration ||
		(s.Verified != nil && s.Pending.Identity.Generation <= s.Verified.Identity.Generation)) {
		return Assessment{}, fmt.Errorf("catalog pending proof must be latest and newer than verified")
	}
	if err := r.validate(); err != nil {
		return Assessment{}, err
	}
	if r.Epoch == nil || *r.Epoch != s.Epoch {
		return Assessment{}, ErrReceiverEpochMismatch
	}
	// validate already checked canonical, bounded int64 counters.
	highest, _ := statusCounter(r.HighestObservedGeneration)
	completed, _ := statusCounter(r.CompletedGeneration)
	if highest > s.AllocatedGeneration {
		return Assessment{}, ErrProducerBehindReceiver
	}
	if s.Pending != nil && highest == int64(s.Pending.Identity.Generation) && completed < highest && r.StagedSnapshots == 0 {
		return Assessment{}, ErrPendingExpired
	}
	if s.Verified != nil && completed < int64(s.Verified.Identity.Generation) {
		return Assessment{}, ErrReceiverBehindVerified
	}
	if s.Pending != nil && completed == int64(s.Pending.Identity.Generation) {
		if *r.CompletedManifest != s.Pending.Manifest {
			return Assessment{}, ErrReceiverProofMismatch
		}
		return Assessment{PendingCompleted: true}, nil
	}
	if s.Verified != nil && completed == int64(s.Verified.Identity.Generation) {
		if *r.CompletedManifest != s.Verified.Manifest {
			return Assessment{}, ErrReceiverProofMismatch
		}
		return Assessment{VerifiedMatches: true, CanDeliver: s.Pending != nil}, nil
	}
	if completed == 0 && s.Verified == nil {
		return Assessment{CanDeliver: s.Pending != nil}, nil
	}
	return Assessment{}, ErrReceiverProofMismatch
}
