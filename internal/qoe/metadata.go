package qoe

import (
	"encoding/json"
	"regexp"
	"unicode/utf8"
)

// Free-form metadata on a telemetry event is the one place where every bound
// this package establishes could be undone at once, so it is deny-by-default —
// the same discipline jobstatus applies to operational job metadata, for the
// same reason and with the same shape.
//
// The threat is not exotic. A player's error object routinely contains the full
// segment URL, and a presigned segment URL IS a bearer credential. An "extras"
// map that forwarded whatever the client sent would put those in a table with
// 7-day retention, readable by any admin, for the sake of a debugging field
// nobody asked for.

// metadataAllowlist is the bounded playback vocabulary. Every key here is a
// small enum or a bounded number that helps explain a measurement without
// identifying anyone or naming a URL.
var metadataAllowlist = map[string]bool{
	// Which rung the engine was on, as a label rather than a height.
	"rendition": true,
	// hls.js's own fatal/non-fatal distinction, so a spike of non-fatal errors
	// is distinguishable from a playback that actually died.
	"fatal": true,
	// Whether the measurement came from a fresh load or a seek.
	"trigger": true,
	// Bounded counters the client already has: how many segments it had
	// buffered, and how many switches it had made.
	"buffered_segments": true,
	"switch_count":      true,
	// Coarse network hint from the Network Information API ("4g", "wifi").
	// Coarse on purpose: it is a class, not a carrier.
	"network": true,
	// Whether the tab was visible. A backgrounded tab produces stalls that are
	// not delivery problems, and being able to exclude them is the difference
	// between a usable p95 and a noisy one.
	"visible": true,
}

var (
	// metadataToken admits only a short, single-token enum or opaque id. This
	// structurally rejects URLs, signed query strings, emails, Bearer values,
	// multiline output and nested serialized payloads — the same regexp shape
	// jobstatus uses, and the reason no separate URL rule is needed.
	metadataToken = regexp.MustCompile(`^[A-Za-z0-9_.:\-]{1,64}$`)
	// sensitiveValue is a second, independent net for anything that slipped
	// through as a "token".
	sensitiveValue = regexp.MustCompile(`(?i)(?:https?|s3|file)://\S+|(?:authorization|cookie|password|token|secret)\s*[:=]\s*\S+`)
	emailValue     = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
)

const (
	// maxMetadataKeys caps how many allowlisted keys one event may carry.
	maxMetadataKeys = 8
	// maxMetadataBytes caps the encoded form, before and after filtering.
	maxMetadataBytes = 2048
)

// SafeMetadata filters a client-supplied map down to the allowlist and returns
// its JSON encoding, always valid and always small. An unparseable, oversized or
// entirely-disallowed map yields "{}" rather than an error: metadata is an
// optional explanation of a measurement, and losing it must never cost the
// measurement.
func SafeMetadata(in map[string]any) json.RawMessage {
	if len(in) == 0 {
		return json.RawMessage(`{}`)
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		if len(out) >= maxMetadataKeys {
			break
		}
		if !metadataAllowlist[key] {
			continue
		}
		if safe, ok := safeMetadataValue(value); ok {
			out[key] = safe
		}
	}
	data, err := json.Marshal(out)
	if err != nil || len(data) > maxMetadataBytes {
		return json.RawMessage(`{}`)
	}
	return data
}

// safeMetadataValue admits booleans, bounded non-negative numbers, and short
// single-token strings. Anything else — arrays, objects, negative or huge
// numbers, long or structured strings — is dropped.
func safeMetadataValue(value any) (any, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case float64:
		// JSON numbers decode to float64. Counters and sizes are non-negative
		// and well below the exact-integer range.
		if v >= 0 && v <= 9_007_199_254_740_991 {
			return v, true
		}
	case json.Number:
		if f, err := v.Float64(); err == nil && f >= 0 && f <= 9_007_199_254_740_991 {
			return f, true
		}
	case string:
		if utf8.ValidString(v) && metadataToken.MatchString(v) &&
			!sensitiveValue.MatchString(v) && !emailValue.MatchString(v) {
			return v, true
		}
	}
	return nil, false
}
