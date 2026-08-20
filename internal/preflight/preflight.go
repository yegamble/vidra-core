// Package preflight is the host-level checks that run BEFORE a deployment does
// something expensive or irreversible, shared between `vidra setup`, `vidra
// doctor` and deploy/deploy.sh (docs/productionization/interfaces.md §1, the
// DependencyManager seam; phase-1 items 11 and 14).
//
// It is a LIBRARY, and deliberately a thin one:
//
//   - it never prints and never exits. Every check returns a typed result with an
//     operator-facing message and a suggested fix, and the CALLER decides whether
//     that is a wizard field error, a ✓/⚠/✗ line in `vidra doctor`, or a refusal
//     in a deploy script. The same finding has to read correctly in all three.
//   - every dependency it has on the outside world — DNS, HTTP — is injectable,
//     so the tests are hermetic. A preflight suite that needs the network to test
//     the network is a suite nobody runs.
//   - a check that cannot COMPLETE is not a check that FAILED. A host with no
//     outbound HTTPS cannot discover its own public IP, and answering "your DNS
//     is wrong" to that question is worse than answering "I could not tell":
//     StatusWarn exists for exactly that gap, and it is why the results carry
//     both a Status and the raw findings the caller can re-interpret.
package preflight

// Status is one preflight outcome. The three values are the three things an
// operator can do about a check: nothing, read it, or fix it before continuing.
type Status int

const (
	// StatusOK: the check ran and found what it expected.
	StatusOK Status = iota
	// StatusWarn: the check could not complete, or completed with a finding that
	// does not stop a deployment. Never used for a check that ran and failed.
	StatusWarn
	// StatusFail: the check ran and found a problem the operator has to fix.
	StatusFail
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusWarn:
		return "warn"
	case StatusFail:
		return "fail"
	default:
		return "unknown"
	}
}
