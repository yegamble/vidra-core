package federation

import (
	"net"
	"strings"

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

// inboundDomainKey is the registrable-domain key of the inbound actor-fetch
// limits: capDomain, except that an IP literal is grouped by its IPv4 /24 or
// IPv6 /48, since one party holding a range is the IP-literal analogue of one
// registrable domain.
func inboundDomainKey(rawURL string) string {
	d := capDomain(rawURL)
	ip := net.ParseIP(strings.Trim(d, "[]"))
	if ip == nil {
		return d
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.Mask(net.CIDRMask(24, 32)).String() + "/24"
	}
	return ip.Mask(net.CIDRMask(48, 128)).String() + "/48"
}
