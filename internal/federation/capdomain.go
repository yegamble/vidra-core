package federation

import (
	"net"

	"golang.org/x/net/publicsuffix"
)

// capDomain is the key for a per-domain budget (an inbound sender's or an
// outbound origin's): the registrable domain
// (eTLD+1) of the URL's normalised host, so sub1.evil.example and
// sub2.evil.example share one budget. An IP literal, or a host the public
// suffix list cannot reduce, keys on the normalised host itself.
func capDomain(rawURL string) string {
	host := hostOf(rawURL)
	if n, ok := NormalizeFollowHost(host); ok {
		host = n
	}
	if net.ParseIP(host) != nil {
		return host
	}
	if d, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return d
	}
	return host
}
