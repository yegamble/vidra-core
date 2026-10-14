package instancemod

import (
	"context"
	"strings"

	"golang.org/x/net/idna"
)

// BlockProbeForms lists every spelling under which an admin may have stored a
// block for host: the punycode form and the Unicode form of an IDN (admin
// entries are stored lower-cased only, so either can be what is on the list).
// The port is not part of the probe: IsInstanceBlocked ignores a stored port.
func BlockProbeForms(host string) []string {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return nil
	}
	forms := []string{host}
	if ascii, err := idna.Lookup.ToASCII(host); err == nil && ascii != host {
		forms = append(forms, ascii)
	}
	if uni, err := idna.Lookup.ToUnicode(host); err == nil && uni != host && uni != "" {
		forms = append(forms, uni)
	}
	return forms
}

// BlockMatches reports whether host is on the block list under any equivalent
// spelling, probing with the supplied lookup (the repository's
// IsInstanceBlocked, which also treats a stored host:port entry as the host).
func BlockMatches(ctx context.Context, isBlocked func(ctx context.Context, domain string) (bool, error), host string) (bool, error) {
	return BlockMatchesPort(ctx, isBlocked, host, "")
}

// BlockMatchesPort is BlockMatches for a host reached on a known port: each
// spelling is also probed as host:port, so an entry stored with its port (dev
// origins, or the scheme's default written out) is found even by a lookup that
// matches exactly. An empty port probes the bare host only.
func BlockMatchesPort(ctx context.Context, isBlocked func(ctx context.Context, domain string) (bool, error), host, port string) (bool, error) {
	for _, f := range BlockProbeForms(host) {
		keys := []string{f}
		if port != "" {
			keys = append(keys, f+":"+port)
		}
		for _, k := range keys {
			blocked, err := isBlocked(ctx, k)
			if err != nil || blocked {
				return blocked, err
			}
		}
	}
	return false, nil
}

// EffectivePort is the port a URL actually connects to: the explicit one, else
// the scheme's default ("" for a scheme with none), so an entry written
// "host:443" covers an https URL that leaves the port implicit.
func EffectivePort(scheme, explicit string) string {
	if explicit != "" {
		return explicit
	}
	switch strings.ToLower(scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}
