package preflight

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// Every test in this file is hermetic: the resolver and the HTTP client are
// fakes, so nothing here depends on the machine's DNS, its network, or whether
// api.ipify.org is up today. A preflight suite that needs the network to test the
// network is a suite that fails for reasons unrelated to the code.
type fakeResolver struct {
	addrs []string
	err   error
	asked []string
}

func (f *fakeResolver) LookupNetIP(_ context.Context, network, host string) ([]netip.Addr, error) {
	f.asked = append(f.asked, network+" "+host)
	if f.err != nil {
		return nil, f.err
	}
	return parseAddrs(f.addrs), nil
}

func parseAddrs(in []string) []netip.Addr {
	out := make([]netip.Addr, 0, len(in))
	for _, s := range in {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// echoClient answers each URL with a canned body/status; an absent URL is a
// transport error, which is what a blocked endpoint looks like.
func echoClient(t *testing.T, answers map[string]struct {
	status int
	body   string
}) (*http.Client, *[]string) {
	t.Helper()
	var asked []string
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		asked = append(asked, r.URL.String())
		a, ok := answers[r.URL.String()]
		if !ok {
			return nil, errors.New("dial tcp: connection refused")
		}
		return &http.Response{
			StatusCode: a.status,
			Body:       io.NopCloser(strings.NewReader(a.body)),
			Header:     http.Header{},
			Request:    r,
		}, nil
	})}, &asked
}

func TestHostReducesEveryDomainSpelling(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"video.example.org", "video.example.org"},
		{"https://video.example.org", "video.example.org"},
		{"https://video.example.org/", "video.example.org"},
		{"https://VIDEO.Example.ORG", "video.example.org"},
		{"video.example.org.", "video.example.org"},
		{"video.example.org:8443", "video.example.org"},
		{"https://video.example.org:8443", "video.example.org"},
		{"  video.example.org  ", "video.example.org"},
	} {
		got, err := Host(tc.in)
		if err != nil {
			t.Errorf("Host(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Host(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	for _, in := range []string{"", "   ", "https://"} {
		if got, err := Host(in); err == nil {
			t.Errorf("Host(%q) = %q, want an error", in, got)
		}
	}
}

// The answers have to be comparable across runs and across address families:
// ::ffff:203.0.113.10 and 203.0.113.10 are ONE address, and a check that called
// them different would send an operator to fix a correct zone file.
func TestLookupHostNormalisesTheAnswer(t *testing.T) {
	r := &fakeResolver{addrs: []string{"203.0.113.20", "::ffff:203.0.113.10", "203.0.113.10", "2001:db8::1"}}
	got, err := LookupHost(context.Background(), r, "https://video.example.org/")
	if err != nil {
		t.Fatalf("LookupHost: %v", err)
	}
	want := []string{"203.0.113.10", "203.0.113.20", "2001:db8::1"}
	if len(got) != len(want) {
		t.Fatalf("LookupHost = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i].String() != w {
			t.Errorf("address %d = %s, want %s", i, got[i], w)
		}
	}
	// The origin was reduced to a host before the lookup, and both families were
	// asked for in one query.
	if len(r.asked) != 1 || r.asked[0] != "ip video.example.org" {
		t.Errorf("resolver was asked %v, want one \"ip video.example.org\"", r.asked)
	}
}

func TestLookupHostReportsTheResolverError(t *testing.T) {
	r := &fakeResolver{err: errors.New("no such host")}
	if _, err := LookupHost(context.Background(), r, "video.example.org"); err == nil || !strings.Contains(err.Error(), "video.example.org") {
		t.Fatalf("err = %v, want a failure naming the host", err)
	}
}

// An operator who gives the address has answered the question: nothing is
// fetched, and a value that is not an address is an error rather than a silent
// fall back to discovery (which would check a different host).
func TestPublicIPPrefersTheOverride(t *testing.T) {
	client, asked := echoClient(t, nil)
	got, err := PublicIP(context.Background(), PublicIPOptions{Override: "203.0.113.10", Client: client, Endpoints: []string{"https://echo.example/"}})
	if err != nil {
		t.Fatalf("PublicIP: %v", err)
	}
	if got.String() != "203.0.113.10" {
		t.Errorf("PublicIP = %s, want the override", got)
	}
	if len(*asked) != 0 {
		t.Errorf("an endpoint was asked despite the override: %v", *asked)
	}

	if _, err := PublicIP(context.Background(), PublicIPOptions{Override: "not-an-ip", Client: client}); err == nil {
		t.Fatal("a bad override was accepted")
	}
}

// One endpoint being down, blocked, or answering with a captive portal's HTML
// must not turn a DNS check into a warning: they are asked in order until one
// answers with something that parses.
func TestPublicIPFallsThroughToTheNextEndpoint(t *testing.T) {
	type answer = struct {
		status int
		body   string
	}
	for _, tc := range []struct {
		name    string
		answers map[string]answer
		want    string
	}{
		{
			name:    "the first is down",
			answers: map[string]answer{"https://second.example/": {200, "203.0.113.10\n"}},
			want:    "203.0.113.10",
		},
		{
			name: "the first errors",
			answers: map[string]answer{
				"https://first.example/":  {503, "unavailable"},
				"https://second.example/": {200, "203.0.113.10"},
			},
			want: "203.0.113.10",
		},
		{
			name: "the first answers with a captive portal",
			answers: map[string]answer{
				"https://first.example/":  {200, "<html><body>Sign in to continue</body></html>"},
				"https://second.example/": {200, "203.0.113.10"},
			},
			want: "203.0.113.10",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := echoClient(t, tc.answers)
			got, err := PublicIP(context.Background(), PublicIPOptions{
				Client:    client,
				Endpoints: []string{"https://first.example/", "https://second.example/"},
			})
			if err != nil {
				t.Fatalf("PublicIP: %v", err)
			}
			if got.String() != tc.want {
				t.Errorf("PublicIP = %s, want %s", got, tc.want)
			}
		})
	}
}

// When every endpoint fails, the error names them and says what to do — a host
// with no outbound HTTPS is a legitimate configuration, not a broken one.
func TestPublicIPFailureNamesEveryEndpointTried(t *testing.T) {
	client, _ := echoClient(t, nil)
	_, err := PublicIP(context.Background(), PublicIPOptions{
		Client:    client,
		Endpoints: []string{"https://first.example/", "https://second.example/"},
		Timeout:   time.Second,
	})
	if err == nil {
		t.Fatal("every endpoint failed and PublicIP returned no error")
	}
	for _, want := range []string{"first.example", "second.example", "Pass it explicitly"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// The composed check, in the four states a caller has to render differently.
func TestCheckDomain(t *testing.T) {
	ctx := context.Background()
	client, _ := echoClient(t, map[string]struct {
		status int
		body   string
	}{"https://echo.example/": {200, "203.0.113.10"}})
	discovery := PublicIPOptions{Client: client, Endpoints: []string{"https://echo.example/"}}

	t.Run("the domain points here", func(t *testing.T) {
		res := CheckDomain(ctx, DomainRequest{
			Domain:   "https://video.example.org",
			Resolver: &fakeResolver{addrs: []string{"203.0.113.10"}},
			PublicIP: discovery,
		})
		if res.Status != StatusOK || !res.Match {
			t.Fatalf("status = %s, match = %v, want ok/true (%+v)", res.Status, res.Match, res)
		}
		if res.Domain != "video.example.org" {
			t.Errorf("Domain = %q, want the host the origin reduces to", res.Domain)
		}
		if res.PublicIP.String() != "203.0.113.10" || res.Fix != "" {
			t.Errorf("a passing check carries a fix or no address: %+v", res)
		}
	})

	t.Run("the domain points somewhere else", func(t *testing.T) {
		res := CheckDomain(ctx, DomainRequest{
			Domain:   "video.example.org",
			Resolver: &fakeResolver{addrs: []string{"198.51.100.7"}},
			PublicIP: discovery,
		})
		if res.Status != StatusFail || res.Match {
			t.Fatalf("status = %s, match = %v, want fail/false", res.Status, res.Match)
		}
		for _, want := range []string{"198.51.100.7", "203.0.113.10", "video.example.org"} {
			if !strings.Contains(res.Message+res.Fix, want) {
				t.Errorf("the operator is not told about %q: %q / %q", want, res.Message, res.Fix)
			}
		}
		// The reason this check exists at all has to be in the fix.
		if !strings.Contains(res.Fix, "Let's Encrypt") {
			t.Errorf("the fix does not say what deploying anyway would cost: %q", res.Fix)
		}
	})

	t.Run("the domain does not resolve", func(t *testing.T) {
		res := CheckDomain(ctx, DomainRequest{
			Domain:   "video.example.org",
			Resolver: &fakeResolver{err: errors.New("no such host")},
			PublicIP: discovery,
		})
		if res.Status != StatusFail || res.Err == nil {
			t.Fatalf("status = %s, err = %v, want fail with the resolver error kept", res.Status, res.Err)
		}
		if !strings.Contains(res.Fix, "A record") {
			t.Errorf("the fix does not say to create the record: %q", res.Fix)
		}
	})

	t.Run("an empty answer is a failure too", func(t *testing.T) {
		res := CheckDomain(ctx, DomainRequest{
			Domain:   "video.example.org",
			Resolver: &fakeResolver{},
			PublicIP: discovery,
		})
		if res.Status != StatusFail || res.Err == nil {
			t.Fatalf("status = %s, err = %v, want a failure for a name with no addresses", res.Status, res.Err)
		}
	})

	t.Run("this host's address is unknown", func(t *testing.T) {
		blocked, _ := echoClient(t, nil)
		res := CheckDomain(ctx, DomainRequest{
			Domain:   "video.example.org",
			Resolver: &fakeResolver{addrs: []string{"203.0.113.10"}},
			PublicIP: PublicIPOptions{Client: blocked, Endpoints: []string{"https://echo.example/"}},
		})
		// A check that could not COMPLETE is not a check that failed: the DNS
		// answer may well be right, and "wrong" would send an operator to edit a
		// correct zone file.
		if res.Status != StatusWarn {
			t.Fatalf("status = %s, want warn when the comparison could not be made", res.Status)
		}
		if len(res.Resolved) != 1 || res.PublicIP.IsValid() || res.Match {
			t.Errorf("the partial finding was not kept: %+v", res)
		}
		if !strings.Contains(res.Fix, "explicitly") {
			t.Errorf("the fix does not offer the way out: %q", res.Fix)
		}
	})

	t.Run("an expected address given by the caller wins", func(t *testing.T) {
		res := CheckDomain(ctx, DomainRequest{
			Domain:   "video.example.org",
			Expected: "198.51.100.7",
			Resolver: &fakeResolver{addrs: []string{"198.51.100.7"}},
			// Discovery would answer 203.0.113.10 and fail the check.
			PublicIP: discovery,
		})
		if res.Status != StatusOK || res.PublicIP.String() != "198.51.100.7" {
			t.Fatalf("status = %s, ip = %s, want the caller's expectation used", res.Status, res.PublicIP)
		}
	})

	t.Run("no domain", func(t *testing.T) {
		res := CheckDomain(ctx, DomainRequest{Resolver: &fakeResolver{}, PublicIP: discovery})
		if res.Status != StatusFail || res.Fix == "" {
			t.Fatalf("an empty domain produced %+v, want a failure with a fix", res)
		}
	})
}

// The domain check is IPv4, end to end, because deploy/deploy.sh is.
//
// deploy.sh resolves A records and nothing else (getent ahostsv4, dig A, host -t
// A), so an AAAA-only deployment that passed here would be refused by the deploy
// minutes later — and this check is what an operator runs to find out WHY the
// deploy refused. The escape hatches both sides honour are VIDRA_PUBLIC_IP and
// VIDRA_SKIP_DNS_PREFLIGHT, and the finding says so.
func TestCheckDomainIsIPv4Only(t *testing.T) {
	ctx := context.Background()
	client, _ := echoClient(t, map[string]struct {
		status int
		body   string
	}{"https://echo.example/": {200, "203.0.113.10"}})
	discovery := PublicIPOptions{Client: client, Endpoints: []string{"https://echo.example/"}}

	t.Run("the resolver is asked for A records", func(t *testing.T) {
		r := &fakeResolver{addrs: []string{"203.0.113.10"}}
		CheckDomain(ctx, DomainRequest{Domain: "video.example.org", Resolver: r, PublicIP: discovery})
		if len(r.asked) != 1 || r.asked[0] != "ip4 video.example.org" {
			t.Errorf("resolver was asked %v, want one \"ip4 video.example.org\" — deploy.sh looks up A records only", r.asked)
		}
	})

	t.Run("a dual-stack domain matches on its A record", func(t *testing.T) {
		res := CheckDomain(ctx, DomainRequest{
			Domain:   "video.example.org",
			Resolver: &fakeResolver{addrs: []string{"2001:db8::1", "203.0.113.10"}},
			PublicIP: discovery,
		})
		if res.Status != StatusOK || !res.Match {
			t.Fatalf("status = %s, match = %v, want the A record compared (%+v)", res.Status, res.Match, res)
		}
		if len(res.Resolved) != 1 {
			t.Errorf("Resolved = %v, want the AAAA answer filtered out", res.Resolved)
		}
	})

	t.Run("an AAAA-only domain fails, and says how to deploy anyway", func(t *testing.T) {
		res := CheckDomain(ctx, DomainRequest{
			Domain:   "video.example.org",
			Resolver: &fakeResolver{addrs: []string{"2001:db8::1"}},
			PublicIP: discovery,
		})
		if res.Status != StatusFail {
			t.Fatalf("status = %s, want the failure deploy.sh would also produce", res.Status)
		}
		for _, want := range []string{"IPv4", "VIDRA_PUBLIC_IP", "VIDRA_SKIP_DNS_PREFLIGHT"} {
			if !strings.Contains(res.Message+res.Fix, want) {
				t.Errorf("the operator is not told about %q: %q / %q", want, res.Message, res.Fix)
			}
		}
	})

	t.Run("an IPv6 public IP is not compared against A records", func(t *testing.T) {
		v6Client, _ := echoClient(t, map[string]struct {
			status int
			body   string
		}{"https://echo.example/": {200, "2001:db8::1"}})
		res := CheckDomain(ctx, DomainRequest{
			Domain:   "video.example.org",
			Resolver: &fakeResolver{addrs: []string{"203.0.113.10"}},
			PublicIP: PublicIPOptions{Client: v6Client, Endpoints: []string{"https://echo.example/"}},
		})
		// A dual-stack host that reached the echo service over IPv6 got its v6
		// address back. No A record can name it, so calling the record wrong
		// would send an operator to edit a correct zone file — and deploy.sh
		// does not compare either, it warns and continues.
		if res.Status != StatusWarn || res.Match {
			t.Fatalf("status = %s, match = %v, want a ⚠ that made no comparison (%+v)", res.Status, res.Match, res)
		}
		if !strings.Contains(res.Fix, "VIDRA_PUBLIC_IP") {
			t.Errorf("the fix does not offer the way out: %q", res.Fix)
		}
	})

	t.Run("an explicit expectation is compared as given", func(t *testing.T) {
		// VIDRA_PUBLIC_IP is the operator's answer, and deploy.sh compares it
		// verbatim: an override that cannot match is a finding, not a shrug.
		res := CheckDomain(ctx, DomainRequest{
			Domain:   "video.example.org",
			Expected: "2001:db8::1",
			Resolver: &fakeResolver{addrs: []string{"203.0.113.10"}},
			PublicIP: discovery,
		})
		if res.Status != StatusFail {
			t.Fatalf("status = %s, want the mismatch reported for an explicit expectation", res.Status)
		}
	})
}

// The public-IP endpoints are a cross-repo contract: deploy/deploy.sh asks the
// same two, in the same order, so doctor and the deploy that follows it compare
// the domain against the same address.
func TestPublicIPSourcesAreTheDeployScriptsEndpoints(t *testing.T) {
	want := []string{"https://api.ipify.org", "https://icanhazip.com"}
	if len(PublicIPSources) != len(want) {
		t.Fatalf("PublicIPSources = %v, want %v", PublicIPSources, want)
	}
	for i, w := range want {
		if PublicIPSources[i] != w {
			t.Errorf("PublicIPSources[%d] = %q, want %q — change deploy/deploy.sh's discover_public_ip in the same release", i, PublicIPSources[i], w)
		}
	}
}

// An IPv4 answer that arrives IPv4-mapped still matches: it is the same host.
func TestCheckDomainMatchesAcrossTheMappedForm(t *testing.T) {
	client, _ := echoClient(t, map[string]struct {
		status int
		body   string
	}{"https://echo.example/": {200, "::ffff:203.0.113.10"}})
	res := CheckDomain(context.Background(), DomainRequest{
		Domain:   "video.example.org",
		Resolver: &fakeResolver{addrs: []string{"203.0.113.10"}},
		PublicIP: PublicIPOptions{Client: client, Endpoints: []string{"https://echo.example/"}},
	})
	if res.Status != StatusOK || !res.Match {
		t.Fatalf("status = %s, match = %v, want the mapped form treated as the same address", res.Status, res.Match)
	}
}

func TestStatusString(t *testing.T) {
	for _, tc := range []struct {
		s    Status
		want string
	}{{StatusOK, "ok"}, {StatusWarn, "warn"}, {StatusFail, "fail"}, {Status(9), "unknown"}} {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("Status(%d).String() = %q, want %q", tc.s, got, tc.want)
		}
	}
}
