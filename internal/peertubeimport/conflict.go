package peertubeimport

import (
	"fmt"
	"strings"
)

// ConflictPolicy resolves collisions between a source entity's natural key
// (username / channel handle / email / playlist slug) and an existing Vidra row.
type ConflictPolicy string

const (
	// PolicySkip (default, safest, non-destructive) leaves the existing Vidra row
	// untouched and maps the source entity to it — the source entity's own row is
	// not created, but its stable id is recorded in the ledger pointing at the
	// existing row so children still resolve.
	PolicySkip ConflictPolicy = "skip"
	// PolicyRename imports the source entity under a de-duplicated identifier
	// (e.g. "alice" → "alice-2"), preserving both rows.
	PolicyRename ConflictPolicy = "rename"
	// PolicyMerge attaches the source entity's children to the existing Vidra row
	// (same effect as skip for the parent, kept distinct so the ledger note and
	// the operator's intent are explicit).
	PolicyMerge ConflictPolicy = "merge"
	// PolicyFail aborts the whole import on the first collision — for operators who
	// want a guaranteed-clean target and will resolve conflicts by hand.
	PolicyFail ConflictPolicy = "fail"
)

// ParseConflictPolicy validates and normalises a policy string. Empty defaults
// to the safest option (skip).
func ParseConflictPolicy(s string) (ConflictPolicy, error) {
	switch ConflictPolicy(strings.ToLower(strings.TrimSpace(s))) {
	case "", PolicySkip:
		return PolicySkip, nil
	case PolicyRename:
		return PolicyRename, nil
	case PolicyMerge:
		return PolicyMerge, nil
	case PolicyFail:
		return PolicyFail, nil
	default:
		return "", fmt.Errorf("peertubeimport: invalid conflict policy %q (want skip|rename|merge|fail)", s)
	}
}

// String renders the policy.
func (p ConflictPolicy) String() string { return string(p) }

// dedupeName produces a candidate identifier not present in taken, by appending
// "-2", "-3", … to base. taken reports whether a candidate already exists (a
// lookup against the destination). It is used by PolicyRename for usernames and
// channel handles. The suffix search is bounded so a pathological instance
// cannot loop forever.
func dedupeName(base string, taken func(candidate string) (bool, error)) (string, error) {
	exists, err := taken(base)
	if err != nil {
		return "", err
	}
	if !exists {
		return base, nil
	}
	for n := 2; n < 10000; n++ {
		cand := fmt.Sprintf("%s-%d", base, n)
		exists, err := taken(cand)
		if err != nil {
			return "", err
		}
		if !exists {
			return cand, nil
		}
	}
	return "", fmt.Errorf("peertubeimport: exhausted rename candidates for %q", base)
}
