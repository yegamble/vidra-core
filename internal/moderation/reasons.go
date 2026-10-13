package moderation

import "slices"

// PredefinedReasons are PeerTube's abuse reason tags, in its display order.
// They are optional structure on top of a report's free-text reason.
var PredefinedReasons = []string{"violentOrRepulsive", "hatefulOrAbusive", "spamOrMisleading",
	"privacy", "rights", "serverRules", "thumbnails", "captions"}

// IsPredefinedReason reports whether r is one of PredefinedReasons.
func IsPredefinedReason(r string) bool { return slices.Contains(PredefinedReasons, r) }

// NormalizeReasons returns the known reasons in rs, de-duplicated, in first-seen
// order, and always non-nil (the column is NOT NULL). Unknown values are
// dropped: inbound federation uses this, where ignoring beats rejecting.
func NormalizeReasons(rs []string) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		if IsPredefinedReason(r) && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// VideoReportDetail is the optional structure of a video report: predefined
// reasons and a [start, end] range in whole seconds. End is open (nil) when only
// a moment is reported.
type VideoReportDetail struct {
	Reasons      []string
	StartSeconds *int32
	EndSeconds   *int32
}

// ValidateRange checks the range against the video's duration (nil = unknown).
// It returns the offending field ("start_seconds"/"end_seconds") and a message,
// or "" when the range is acceptable.
func (d VideoReportDetail) ValidateRange(durationSeconds *int32) (field, msg string) {
	switch {
	case d.StartSeconds == nil && d.EndSeconds == nil:
		return "", ""
	case d.StartSeconds == nil:
		return "start_seconds", "is required when end_seconds is set"
	case *d.StartSeconds < 0:
		return "start_seconds", "must be 0 or more"
	case d.EndSeconds != nil && *d.EndSeconds <= *d.StartSeconds:
		return "end_seconds", "must be greater than start_seconds"
	case durationSeconds != nil && *durationSeconds > 0 && d.EndSeconds != nil && *d.EndSeconds > *durationSeconds:
		return "end_seconds", "must be within the video's duration"
	case durationSeconds != nil && *durationSeconds > 0 && *d.StartSeconds > *durationSeconds:
		return "start_seconds", "must be within the video's duration"
	}
	return "", ""
}
