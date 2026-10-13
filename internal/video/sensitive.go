package video

import "sort"

// Sensitive-content flags (migration 0173): the KIND of sensitive a video is,
// mirroring PeerTube 7.2's nsfwFlags. "sex" is PeerTube's explicitSex on the
// wire (federation maps it).
const (
	SensitiveFlagViolent  = "violent"
	SensitiveFlagShocking = "shocking"
	SensitiveFlagSex      = "sex"
)

// SensitiveFlags is the closed set, in display order.
var SensitiveFlags = []string{SensitiveFlagViolent, SensitiveFlagShocking, SensitiveFlagSex}

// sensitivePolicyRank orders the viewer policies by restrictiveness.
var sensitivePolicyRank = map[string]int{"display": 0, "blur": 1, "warn": 2, "hide": 3}

// IsSensitiveFlag reports whether f is a known flag.
func IsSensitiveFlag(f string) bool {
	for _, k := range SensitiveFlags {
		if f == k {
			return true
		}
	}
	return false
}

// IsSensitivePolicy reports whether p is a viewer policy value.
func IsSensitivePolicy(p string) bool { _, ok := sensitivePolicyRank[p]; return ok }

// NormalizeSensitiveFlags drops unknown and duplicate flags and returns the
// rest in canonical order, never nil (the column is NOT NULL).
func NormalizeSensitiveFlags(in []string) []string {
	have := map[string]bool{}
	for _, f := range in {
		if IsSensitiveFlag(f) {
			have[f] = true
		}
	}
	out := make([]string, 0, len(have))
	for _, f := range SensitiveFlags {
		if have[f] {
			out = append(out, f)
		}
	}
	return out
}

// ResolveSensitivePolicy is the policy a viewer's settings give one video: the
// MOST RESTRICTIVE of the viewer's policy for each flag the video carries
// (override, else base), or base for a sensitive video with no flags. A video
// that is not sensitive resolves to "display". It must agree with the SQL
// vidra_sensitive_hidden() on "hide" — that is the enforced half.
func ResolveSensitivePolicy(base string, overrides map[string]string, flags []string, isSensitive bool) string {
	if !isSensitive {
		return "display"
	}
	if len(flags) == 0 {
		return base
	}
	ps := make([]string, 0, len(flags))
	for _, f := range flags {
		p := base
		if o, ok := overrides[f]; ok && IsSensitivePolicy(o) {
			p = o
		}
		ps = append(ps, p)
	}
	sort.Slice(ps, func(i, j int) bool { return sensitivePolicyRank[ps[i]] > sensitivePolicyRank[ps[j]] })
	return ps[0]
}

func sensitiveWithFlags(is *bool, flags *[]string) *bool {
	if flags != nil && len(NormalizeSensitiveFlags(*flags)) > 0 {
		t := true
		return &t
	}
	return is
}

// sensitiveFlagsPatch is the flags column's new value, nil = unchanged. Turning
// is_sensitive off without naming flags clears them too: is_sensitive is the
// aggregate, and stale flags on a non-sensitive video would still be federated.
func sensitiveFlagsPatch(is *bool, flags *[]string) []string {
	if flags != nil {
		return NormalizeSensitiveFlags(*flags)
	}
	if is != nil && !*is {
		return []string{}
	}
	return nil
}
