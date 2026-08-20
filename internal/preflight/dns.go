package preflight

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"
)

// The DNS preflight: does the instance's domain resolve to THIS host?
//
// It is the check that has to happen before Caddy is allowed to place an ACME
// order. Let's Encrypt validates over the public internet, so an order for a
// domain whose A record points somewhere else does not fail politely — it fails,
// gets retried by the next deploy, and the failures count against a rate limit
// (5 duplicate certificates per week, 5 validation failures per hostname per
// hour) that then locks the operator out of the CA for the rest of the week. The
// cheap DNS lookup below is what turns that into a one-line refusal.

// DefaultPublicIPTimeout bounds each public-IP lookup. It is short on purpose:
// this runs in front of an install, and an operator watching a prompt will not
// wait 30 seconds to be told a check could not complete.
const DefaultPublicIPTimeout = 3 * time.Second

// maxPublicIPBody is all an IP echo can possibly need. Anything longer is not an
// IP echo — a captive portal, an error page, a proxy — and reading it whole
// would be reading an attacker-controlled body into memory for no reason.
const maxPublicIPBody = 64

// PublicIPSources are the endpoints PublicIP asks when it is not told the
// answer. Two, from different operators, because one being down (or blocked, or
// hijacked by a captive portal) must not turn a DNS check into a warning.
var PublicIPSources = []string{
	"https://api.ipify.org",
	"https://icanhazip.com",
}

// Resolver is the DNS lookup this package needs. *net.Resolver satisfies it, and
// it is an interface so the tests never touch a real nameserver: a preflight
// suite whose results depend on the machine it runs on tells nobody anything.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// PublicIPOptions is how a caller controls (and a test replaces) public-IP
// discovery. The zero value is the production configuration: no override, the
// well-known endpoints, a plain client and the default timeout.
type PublicIPOptions struct {
	// Override is the answer, given rather than discovered. It WINS over
	// everything else: an operator who knows the host's address (a NAT'd box, a
	// floating IP that is not the egress address) is not overruled by an echo
	// service, and a bad override is an error rather than a silent fallback.
	Override string
	// Endpoints replaces PublicIPSources. Order is the order they are asked.
	Endpoints []string
	// Client is the HTTP client; nil means a plain one.
	Client *http.Client
	// Timeout bounds EACH endpoint; zero means DefaultPublicIPTimeout.
	Timeout time.Duration
}

// DomainRequest is one composed domain check.
type DomainRequest struct {
	// Domain is the instance's domain, as a bare host or a full origin — the
	// PUBLIC_BASE_URL value is accepted as-is.
	Domain string
	// Expected is this host's public IP when the caller already knows it. Empty
	// means discover it through PublicIP.
	Expected string
	// Resolver is the DNS resolver; nil means net.DefaultResolver.
	Resolver Resolver
	// PublicIP configures discovery, and is ignored when Expected is set.
	PublicIP PublicIPOptions
}

// DomainResult is what the domain check found. Message and Fix are the operator's
// half — one sentence of finding, one of what to do — and the fields above them
// are the caller's, for a doctor that wants to print the addresses or a wizard
// that wants to offer the discovered IP as a form default.
type DomainResult struct {
	Status Status
	// Domain is the HOST that was looked up, after an origin was reduced to it.
	Domain string
	// Resolved is every A/AAAA address, deduplicated and sorted.
	Resolved []netip.Addr
	// PublicIP is this host's address; the zero value means it is not known.
	PublicIP netip.Addr
	// Match reports whether PublicIP is among Resolved. It is false whenever the
	// comparison could not be made, so it must be read with Status.
	Match bool
	// Message is the finding, addressed to an operator.
	Message string
	// Fix is what to do about it, empty when there is nothing to do.
	Fix string
	// Err is the underlying failure when the check could not complete.
	Err error
}

// CheckDomain is the composed preflight: resolve the domain, find out what this
// host's public address is, and say whether they agree. It never returns an
// error — an error IS one of the results — because every caller has to print
// something either way.
func CheckDomain(ctx context.Context, req DomainRequest) DomainResult {
	res := DomainResult{Domain: strings.TrimSpace(req.Domain)}

	host, err := Host(req.Domain)
	if err != nil {
		res.Status, res.Err = StatusFail, err
		res.Message = "there is no domain to check"
		res.Fix = "set the instance's public domain (PUBLIC_BASE_URL) before deploying: it is the hostname the certificate is issued for"
		return res
	}
	res.Domain = host

	res.Resolved, err = LookupHost(ctx, req.Resolver, host)
	if err != nil || len(res.Resolved) == 0 {
		res.Status, res.Err = StatusFail, err
		if err == nil {
			res.Err = fmt.Errorf("preflight: %s has no A or AAAA record", host)
		}
		res.Message = fmt.Sprintf("%s does not resolve to any address", host)
		res.Fix = fmt.Sprintf("create an A record for %s pointing at this host's public IP (and an AAAA record if it has IPv6), then wait for it to propagate before deploying — Let's Encrypt validates over the public internet, and failed orders count against a per-hostname rate limit", host)
		return res
	}

	public, err := publicIP(ctx, req)
	if err != nil {
		// The check did not fail, it did not COMPLETE: the DNS answer is known
		// and may well be right. Saying "wrong" here would send an operator to
		// edit a correct zone file.
		res.Status, res.Err = StatusWarn, err
		res.Message = fmt.Sprintf("%s resolves to %s, but this host's own public IP could not be determined, so the two were not compared", host, joinAddrs(res.Resolved))
		res.Fix = "pass this host's public IP explicitly (a host behind a proxy, or with no outbound HTTPS, cannot discover it)"
		return res
	}
	res.PublicIP = public
	res.Match = slices.Contains(res.Resolved, public)
	if !res.Match {
		res.Status = StatusFail
		res.Message = fmt.Sprintf("%s resolves to %s, which is not this host (%s)", host, joinAddrs(res.Resolved), public)
		res.Fix = fmt.Sprintf("point %s at %s and wait for the old record's TTL to expire. Deploying now would make Caddy order a certificate it cannot validate, and repeated failures lock this hostname out of Let's Encrypt for the rest of the hour", host, public)
		return res
	}
	res.Status = StatusOK
	res.Message = fmt.Sprintf("%s resolves to %s, which is this host", host, public)
	return res
}

// LookupHost resolves a host's A and AAAA records, returning them deduplicated
// and sorted so two runs of the same check produce the same message. It accepts
// the same spellings Host does.
func LookupHost(ctx context.Context, r Resolver, host string) ([]netip.Addr, error) {
	name, err := Host(host)
	if err != nil {
		return nil, err
	}
	if r == nil {
		r = net.DefaultResolver
	}
	addrs, err := r.LookupNetIP(ctx, "ip", name)
	if err != nil {
		return nil, fmt.Errorf("preflight: resolve %s: %w", name, err)
	}
	return normalize(addrs), nil
}

// PublicIP is this host's address as the internet sees it: the override if there
// is one, otherwise the first endpoint that answers with something that parses.
//
// The endpoints are asked in order and a failure is not fatal until they have all
// failed, because the failure modes are independent — a service that is down, a
// network that blocks it, a captive portal that answers everything with HTML.
// What IS fatal is an override that is not an address: the operator answered the
// question, and falling back to a guess would silently check the wrong thing.
func PublicIP(ctx context.Context, opt PublicIPOptions) (netip.Addr, error) {
	if raw := strings.TrimSpace(opt.Override); raw != "" {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("preflight: %q is not an IP address — it was given as this host's public IP, so there is nothing to fall back to", raw)
		}
		return addr.Unmap(), nil
	}
	endpoints := opt.Endpoints
	if len(endpoints) == 0 {
		endpoints = PublicIPSources
	}
	client := opt.Client
	if client == nil {
		client = &http.Client{}
	}
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = DefaultPublicIPTimeout
	}
	failures := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		addr, err := fetchIP(ctx, client, endpoint, timeout)
		if err == nil {
			return addr, nil
		}
		failures = append(failures, endpoint+" ("+err.Error()+")")
	}
	if len(failures) == 0 {
		return netip.Addr{}, errors.New("preflight: no public-IP endpoint to ask")
	}
	return netip.Addr{}, fmt.Errorf("preflight: could not determine this host's public IP: %s. Pass it explicitly if this host has no outbound HTTPS", strings.Join(failures, "; "))
}

// Host reduces anything that names the instance to the hostname a resolver
// takes: a bare host, a host:port, or a full origin. The trailing dot and the
// case go too — they are the same host to DNS, and keeping them would make two
// spellings of one answer look like two different findings.
func Host(value string) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return "", errors.New("preflight: no domain given")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("preflight: %q is not a usable origin: %w", value, err)
		}
		raw = u.Host
	}
	raw = strings.TrimSuffix(raw, "/")
	if h, _, err := net.SplitHostPort(raw); err == nil {
		raw = h
	}
	raw = strings.TrimSuffix(strings.ToLower(raw), ".")
	if raw == "" {
		return "", fmt.Errorf("preflight: %q has no hostname in it", value)
	}
	return raw, nil
}

// publicIP is the discovery half of CheckDomain: the request's explicit
// expectation, else the options.
func publicIP(ctx context.Context, req DomainRequest) (netip.Addr, error) {
	opt := req.PublicIP
	if expected := strings.TrimSpace(req.Expected); expected != "" {
		opt.Override = expected
	}
	return PublicIP(ctx, opt)
}

func fetchIP(ctx context.Context, client *http.Client, endpoint string, timeout time.Duration) (netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return netip.Addr{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPublicIPBody))
	if err != nil {
		return netip.Addr{}, err
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(string(body)))
	if err != nil {
		return netip.Addr{}, errors.New("the response is not an IP address")
	}
	return addr.Unmap(), nil
}

// normalize makes two resolutions of the same name comparable: IPv4-mapped IPv6
// answers become their IPv4 form (::ffff:203.0.113.10 and 203.0.113.10 are one
// address, and a check that called them different would be wrong), duplicates
// go, and the order is fixed.
func normalize(addrs []netip.Addr) []netip.Addr {
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		a = a.Unmap().WithZone("")
		if !a.IsValid() || slices.Contains(out, a) {
			continue
		}
		out = append(out, a)
	}
	slices.SortFunc(out, func(a, b netip.Addr) int { return a.Compare(b) })
	return out
}

func joinAddrs(addrs []netip.Addr) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		parts = append(parts, a.String())
	}
	return strings.Join(parts, ", ")
}
