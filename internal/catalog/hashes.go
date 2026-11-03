// Package catalog defines immutable content identities for catalogue snapshots.
// Hash format and golden vectors are shared with vidra-search/internal/catalog.
package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// DocumentsDigest canonicalizes the raw array, not a typed struct whose field
// order or omitted optional fields could differ between producer and receiver.
// Business validation is separate: [] has a hash but is not a valid page.
func DocumentsDigest(raw []byte) ([]byte, string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber() // Catalog counters must not round through float64.
	var documents []any
	if err := decoder.Decode(&documents); err != nil || documents == nil {
		return nil, "", fmt.Errorf("catalog: documents must be a JSON array")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, "", fmt.Errorf("catalog: unexpected JSON after documents")
	}
	canonical, err := json.Marshal(documents)
	if err != nil {
		return nil, "", fmt.Errorf("catalog: encode canonical documents: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(digest[:]), nil
}

// PageDigest identifies one immutable page's canonical document content.
type PageDigest struct {
	PageIndex     int32  `json:"page_index"`
	ContentSHA256 string `json:"content_sha256"`
}

// PagesDigest hashes the exact contiguous page sequence while preserving the
// caller's arrival order. A count alone cannot prove which content completed.
func PagesDigest(pages []PageDigest) (string, error) {
	ordered := append([]PageDigest(nil), pages...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].PageIndex < ordered[j].PageIndex })
	digest := sha256.New()
	for i, page := range ordered {
		if page.PageIndex != int32(i) || len(page.ContentSHA256) != sha256.Size*2 {
			return "", fmt.Errorf("catalog: incomplete or invalid page sequence")
		}
		if _, err := hex.DecodeString(page.ContentSHA256); err != nil || strings.ToLower(page.ContentSHA256) != page.ContentSHA256 {
			return "", fmt.Errorf("catalog: invalid canonical page digest")
		}
		_, _ = fmt.Fprintf(digest, "%d:%s\n", page.PageIndex, page.ContentSHA256)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// MembershipDigest hashes sorted canonical UUIDs, each followed by a newline.
// Duplicate and zero IDs cannot establish complete catalogue membership.
func MembershipDigest(ids []uuid.UUID) (string, error) {
	ordered := make([]string, 0, len(ids))
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if id == uuid.Nil || seen[id] {
			return "", fmt.Errorf("catalog: invalid or duplicate membership ID")
		}
		seen[id] = true
		ordered = append(ordered, id.String())
	}
	sort.Strings(ordered)
	digest := sha256.New()
	for _, id := range ordered {
		_, _ = digest.Write([]byte(id + "\n"))
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
