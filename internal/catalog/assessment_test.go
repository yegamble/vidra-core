package catalog

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func assessmentFixture(t *testing.T) (SourceEvidence, Status) {
	t.Helper()
	r, err := ParseStatus([]byte(emptyStatus))
	if err != nil {
		t.Fatal(err)
	}
	epoch, now := uuid.New(), time.Now().UTC()
	m := Manifest{PageCount: 1, DocumentCount: 2, MembershipSHA256: strings.Repeat("a", 64), PagesSHA256: strings.Repeat("b", 64)}
	v := &FullProof{Identity: Identity{ScopeLocal, epoch, 1}, Manifest: m}
	p := &FullProof{Identity: Identity{ScopeLocal, epoch, 2}, Manifest: m}
	r.Epoch, r.Ready, r.CompletedAt, r.CompletedManifest = &epoch, true, &now, &m
	r.HighestObservedGeneration, r.CompletedGeneration = "1", "1"
	return SourceEvidence{Epoch: epoch, AllocatedGeneration: 2, Verified: v, Pending: p}, r
}

func noReceiverCompletion(r *Status) {
	r.Ready, r.CompletedAt, r.CompletedManifest = false, nil, nil
	r.HighestObservedGeneration, r.CompletedGeneration = "0", "0"
}

func TestAssessReceiverNormalEvidenceAndRecoveryRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*SourceEvidence, *Status)
		want   Assessment
		err    error
	}{
		{"verified with pending", func(*SourceEvidence, *Status) {}, Assessment{VerifiedMatches: true, CanDeliver: true}, nil},
		{"retired allocator gap", func(s *SourceEvidence, _ *Status) { s.Pending = nil }, Assessment{VerifiedMatches: true}, nil},
		{"newer staging", func(_ *SourceEvidence, r *Status) {
			r.HighestObservedGeneration = "2"
			r.StagedSnapshots = 1
			r.OldestStagedAt = r.CompletedAt
		}, Assessment{VerifiedMatches: true, CanDeliver: true}, nil},
		{"pending completed", func(_ *SourceEvidence, r *Status) { r.HighestObservedGeneration = "2"; r.CompletedGeneration = "2" }, Assessment{PendingCompleted: true}, nil},
		{"initial pending", func(s *SourceEvidence, r *Status) { s.Verified = nil; noReceiverCompletion(r) }, Assessment{CanDeliver: true}, nil},
		{"empty initialized", func(s *SourceEvidence, r *Status) {
			s.Verified = nil
			s.Pending = nil
			s.AllocatedGeneration = 0
			noReceiverCompletion(r)
		}, Assessment{}, nil},
		{"retired initial gap", func(s *SourceEvidence, r *Status) {
			s.Verified = nil
			s.Pending = nil
			noReceiverCompletion(r)
			r.HighestObservedGeneration = "2"
		}, Assessment{}, nil},
		{"uninitialized receiver", func(_ *SourceEvidence, r *Status) { noReceiverCompletion(r); r.Epoch = nil }, Assessment{}, ErrReceiverEpochMismatch},
		{"different epoch", func(_ *SourceEvidence, r *Status) { e := uuid.New(); r.Epoch = &e }, Assessment{}, ErrReceiverEpochMismatch},
		{"producer restored behind staging", func(_ *SourceEvidence, r *Status) {
			r.HighestObservedGeneration = "3"
			r.StagedSnapshots = 1
			r.OldestStagedAt = r.CompletedAt
		}, Assessment{}, ErrProducerBehindReceiver},
		{"expired pending", func(_ *SourceEvidence, r *Status) { r.HighestObservedGeneration = "2" }, Assessment{}, ErrPendingExpired},
		{"expiry precedes behind", func(_ *SourceEvidence, r *Status) { noReceiverCompletion(r); r.HighestObservedGeneration = "2" }, Assessment{}, ErrPendingExpired},
		{"receiver restored behind verified", func(_ *SourceEvidence, r *Status) { noReceiverCompletion(r) }, Assessment{}, ErrReceiverBehindVerified},
		{"unretained completion", func(s *SourceEvidence, _ *Status) { s.Verified = nil }, Assessment{}, ErrReceiverProofMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r := assessmentFixture(t)
			tc.change(&s, &r)
			got, err := AssessReceiver(s, r)
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("assessment %+v/%v, want %+v/%v", got, err, tc.want, tc.err)
			}
		})
	}
}

func TestAssessReceiverRequiresExactManifestForBothProofs(t *testing.T) {
	for _, pending := range []bool{false, true} {
		for _, mutate := range []func(*Manifest){
			func(m *Manifest) { m.PageCount = 2 },
			func(m *Manifest) { m.DocumentCount++ },
			func(m *Manifest) { m.MembershipSHA256 = strings.Repeat("c", 64) },
			func(m *Manifest) { m.PagesSHA256 = strings.Repeat("c", 64) },
		} {
			s, r := assessmentFixture(t)
			if pending {
				r.CompletedGeneration, r.HighestObservedGeneration = "2", "2"
			}
			mutate(r.CompletedManifest)
			if got, err := AssessReceiver(s, r); got != (Assessment{}) || !errors.Is(err, ErrReceiverProofMismatch) {
				t.Fatalf("mismatched full granted evidence: %+v/%v", got, err)
			}
		}
	}
}

func TestAssessReceiverRejectsInvalidSourceAndStatusWithoutFlags(t *testing.T) {
	for _, mutate := range []func(*SourceEvidence, *Status){
		func(s *SourceEvidence, _ *Status) { s.Epoch = uuid.Nil },
		func(s *SourceEvidence, _ *Status) { s.AllocatedGeneration = -1 },
		func(s *SourceEvidence, _ *Status) { s.Verified.Identity.Scope = "remote" },
		func(s *SourceEvidence, _ *Status) { s.Verified.Identity.Epoch = uuid.New() },
		func(s *SourceEvidence, _ *Status) { s.Verified.Identity.Generation = 0 },
		func(s *SourceEvidence, _ *Status) { s.Verified.Identity.Generation = 3 },
		func(s *SourceEvidence, _ *Status) { s.Pending.Identity.Scope = "remote" },
		func(s *SourceEvidence, _ *Status) { s.Pending.Identity.Epoch = uuid.New() },
		func(s *SourceEvidence, _ *Status) { s.Pending.Identity.Generation = 1 },
		func(s *SourceEvidence, _ *Status) { s.Pending.Identity.Generation = 3 },
		func(s *SourceEvidence, _ *Status) { s.Pending.Manifest.PagesSHA256 = "" },
		func(_ *SourceEvidence, r *Status) { r.ProtocolVersion = 1 },
		func(_ *SourceEvidence, r *Status) { r.Ready = false },
		func(_ *SourceEvidence, r *Status) { r.CompletedGeneration = "01" },
		func(_ *SourceEvidence, r *Status) { r.HighestObservedGeneration = "0" },
		func(_ *SourceEvidence, r *Status) { r.Limits.MaxPages = 0 },
		func(_ *SourceEvidence, r *Status) { r.CompletedAt = nil },
		func(_ *SourceEvidence, r *Status) { r.StagedSnapshots = 1 },
	} {
		s, r := assessmentFixture(t)
		mutate(&s, &r)
		if got, err := AssessReceiver(s, r); err == nil || got != (Assessment{}) {
			t.Fatalf("invalid evidence granted flags: %+v/%v", got, err)
		}
	}
}

func TestAssessReceiverKeepsExactLargeGenerationAndEmptyFull(t *testing.T) {
	s, r := assessmentFixture(t)
	const generation = 9007199254740993
	s.AllocatedGeneration = generation
	s.Verified.Identity.Generation = generation
	s.Pending = nil
	r.CompletedGeneration = strconv.FormatInt(generation, 10)
	r.HighestObservedGeneration = r.CompletedGeneration
	const digest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	empty := Manifest{MembershipSHA256: digest, PagesSHA256: digest}
	s.Verified.Manifest, r.CompletedManifest = empty, &empty
	if got, err := AssessReceiver(s, r); err != nil || got != (Assessment{VerifiedMatches: true}) {
		t.Fatalf("exact large generation/empty full rejected: %+v/%v", got, err)
	}
}
