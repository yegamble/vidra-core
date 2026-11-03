// Package catalog defines the immutable full-observation wire contract shared
// with vidra-search. These values do not enable catalogue delivery.
package catalog

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const (
	ProtocolVersion  = 2
	ScopeLocal       = "local"
	MaxRequestBytes  = 1 << 20
	TypeSnapshotPage = "catalog.snapshot.page"
	TypeSnapshotEnd  = "catalog.snapshot.end"
)

// Generation is an allocated observation sequence, encoded as a decimal string
// so intermediaries cannot round values above JavaScript's exact integer range.
// Status counters that may be zero must not use this type.
type Generation int64

func (g Generation) MarshalJSON() ([]byte, error) {
	if g <= 0 {
		return nil, fmt.Errorf("catalog generation must be positive")
	}
	return json.Marshal(strconv.FormatInt(int64(g), 10))
}

func (g *Generation) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("catalog generation must be a decimal string: %w", err)
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 || strconv.FormatInt(n, 10) != value {
		return fmt.Errorf("catalog generation must be a canonical positive int64")
	}
	*g = Generation(n)
	return nil
}

type Identity struct {
	Scope      string     `json:"scope"`
	Epoch      uuid.UUID  `json:"epoch"`
	Generation Generation `json:"generation"`
}

func (i Identity) Validate() error {
	if i.Scope != ScopeLocal || i.Epoch == uuid.Nil || i.Generation <= 0 {
		return fmt.Errorf("catalog identity requires local scope, nonzero epoch and positive generation")
	}
	return nil
}

type Manifest struct {
	PageCount        int32  `json:"page_count"`
	DocumentCount    int64  `json:"document_count"`
	MembershipSHA256 string `json:"membership_sha256"`
	PagesSHA256      string `json:"pages_sha256"`
}

// Validate checks shape only. Completion must also verify the frozen pages,
// their exact indexes and distinct IDs against both digests.
func (m Manifest) Validate() error {
	if m.PageCount < 0 || m.DocumentCount < 0 || int64(m.PageCount) > m.DocumentCount || (m.PageCount == 0) != (m.DocumentCount == 0) {
		return fmt.Errorf("catalog manifest has inconsistent counts")
	}
	for _, digest := range []string{m.MembershipSHA256, m.PagesSHA256} {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != 32 || digest != strings.ToLower(digest) {
			return fmt.Errorf("catalog manifest requires lowercase SHA-256 digests")
		}
	}
	const empty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if m.PageCount == 0 && (m.MembershipSHA256 != empty || m.PagesSHA256 != empty) {
		return fmt.Errorf("empty catalog manifest requires empty digests")
	}
	return nil
}
