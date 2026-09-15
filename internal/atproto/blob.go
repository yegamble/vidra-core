package atproto

import (
	"encoding/json"
	"errors"
	"sort"
)

// errNonCanonicalBlob is returned by normalizeBlob when a PDS uploadBlob response
// carries no canonical IPLD blob ref anywhere it looks. It is best-effort at the
// call site: the cross-post proceeds without a card image rather than failing.
var errNonCanonicalBlob = errors.New("atproto: uploadBlob response has no canonical blob ref")

// normalizeBlob coerces a PDS uploadBlob result into the canonical IPLD blob ref
//
//	{"$type":"blob","ref":{"$link":<cid>},"mimeType":<mime>,"size":<n>}
//
// so it can be embedded in a createRecord as embed.external.thumb.
//
// A spec-compliant PDS (real Bluesky) already returns exactly this shape and it
// is returned verbatim — the compliant path is unchanged, byte for byte.
//
// This is defence-in-depth for a NON-compliant PDS whose top-level blob lacks
// "$type":"blob" but nests the valid typed blob one level down (e.g. under
// "original"). Embedding such a response verbatim makes createRecord reject the
// write with "embed/external/thumb should be a blob ref". When the top level is
// not itself a canonical blob, normalizeBlob lifts the first nested object that
// IS one (checking "original" first, then remaining keys in a deterministic
// order) and returns that, verbatim.
//
// It never fabricates a blob: if nothing canonical is found it returns
// errNonCanonicalBlob and the caller posts without a card image.
func normalizeBlob(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, errNonCanonicalBlob
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	// Already canonical: return the original bytes untouched (compliant path).
	if isCanonicalBlob(obj) {
		return raw, nil
	}
	// Non-compliant: lift a nested typed blob. Prefer the documented "original"
	// wrapper, then fall back to the remaining keys in sorted order so the choice
	// is deterministic regardless of Go's map iteration order.
	keys := make([]string, 0, len(obj))
	for k := range obj {
		if k != "original" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if _, ok := obj["original"]; ok {
		keys = append([]string{"original"}, keys...)
	}
	for _, k := range keys {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(obj[k], &nested); err != nil {
			continue // not an object; cannot hold a typed blob
		}
		if isCanonicalBlob(nested) {
			return obj[k], nil
		}
	}
	return nil, errNonCanonicalBlob
}

// isCanonicalBlob reports whether obj is an IPLD blob ref: it carries
// "$type":"blob" and a "ref" with a non-empty "$link" (the CID). mimeType/size
// are part of the shape but the discriminator the PDS enforces is the typed ref,
// so those are not required here.
func isCanonicalBlob(obj map[string]json.RawMessage) bool {
	rawType, ok := obj["$type"]
	if !ok {
		return false
	}
	var typ string
	if err := json.Unmarshal(rawType, &typ); err != nil || typ != "blob" {
		return false
	}
	rawRef, ok := obj["ref"]
	if !ok {
		return false
	}
	var ref struct {
		Link string `json:"$link"`
	}
	if err := json.Unmarshal(rawRef, &ref); err != nil || ref.Link == "" {
		return false
	}
	return true
}
